package admin

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/chzyer/readline"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/rksht/uknow"
	cmdcommon "github.com/rksht/uknow/cmd"
	"github.com/rksht/uknow/hand_reader"
	"github.com/rksht/uknow/internal/messages"
	"github.com/rksht/uknow/internal/utils"
	"golang.org/x/sync/errgroup"
)

var (
	envConfig EnvConfig

	errorWaitingForAcks    = errors.New("waiting for acks")
	errorInvalidAdminState = errors.New("invalid admin state")
	errorUnknownPlayer     = errors.New("unknown player")
)

type AdminState string

const (
	AddingPlayers                     AdminState = "adding_players"
	ReadyToServeCards                 AdminState = "ready_to_serve_cards"
	CardsServed                       AdminState = "cards_served"
	PlayerChosenForTurn               AdminState = "player_chosen"
	WaitingForPlayerDecision          AdminState = "waiting_for_player_decision"
	WaitingForChallengePlayerDecision AdminState = "waiting_for_challenge"
	SyncingPlayerDecision             AdminState = "syncing_player_decision"
	DoneSyncingPlayerDecision         AdminState = "done_syncing_player_decision"
	HaveWinner                        AdminState = "have_winner"
)

func playerWithAddress(addr utils.TCPAddress, listenAddrOfPlayer map[string]utils.TCPAddress) (string, error) {
	for playerName, valAddr := range listenAddrOfPlayer {
		if valAddr.Host == addr.Host {
			return playerName, nil
		}
	}
	return "", fmt.Errorf("%w: no player with address %s", errorUnknownPlayer, addr.BindString())
}

func makeAckIdConnectedPlayer(ackerPlayer, connectedPlayer string) string {
	return fmt.Sprintf("%s_connected_to_%s", ackerPlayer, connectedPlayer)
}

func makeAckIdWaitingForPlayerDecision(ackerPlayer string, decisionCounter int) string {
	return fmt.Sprintf("waiting_for_decision.%s.%d", ackerPlayer, decisionCounter)
}

const pauseBeforeChoosingPlayer = 2 * time.Second

type Admin struct {
	table      *uknow.Table
	stateMutex sync.Mutex
	state      AdminState

	aesCipher *uknow.AESCipher

	// Address of player registered on connect command
	listenAddrOfPlayer      map[string]utils.TCPAddress
	shuffler                string
	setReadyPlayer          string
	httpClient              *http.Client
	httpServer              *http.Server
	logger                  *log.Logger
	decisionEventsCompleted int

	expectedAcksList *expectedAcksList
	readlinePrompt   string
	rl               *readline.Instance
}

type ConfigNewAdmin struct {
	ListenAddr     utils.TCPAddress
	Table          *uknow.Table
	SetReadyPlayer string
	aesCipher      *uknow.AESCipher
}

const logFilePrefix = "admin"

func NewAdmin(config *ConfigNewAdmin) *Admin {
	logger := uknow.CreateFileLogger(false, logFilePrefix)
	admin := &Admin{
		table:              config.Table,
		listenAddrOfPlayer: make(map[string]utils.TCPAddress),
		shuffler:           "",
		aesCipher:          config.aesCipher,
		state:              AddingPlayers,
		expectedAcksList:   newExpectedAcksState(logger),
		logger:             logger,
		setReadyPlayer:     config.SetReadyPlayer,
	}

	admin.httpClient = utils.CreateHTTPClient()

	r := admin.setRouterHandlers()

	admin.httpServer = &http.Server{
		Handler:           r,
		Addr:              config.ListenAddr.BindString(),
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       1 * time.Minute,
		ReadHeaderTimeout: 2 * time.Second,
	}

	return admin
}

func (admin *Admin) setRouterHandlers() *mux.Router {
	r := mux.NewRouter()
	r.Path("/player").Methods("POST").HandlerFunc(admin.handleAddNewPlayer)
	r.Path("/ack_player_added").Methods("POST").HandlerFunc(admin.handleAckNewPlayerAdded)
	r.Path("/set_ready").Methods("POST").HandlerFunc(admin.handleSetReady)
	r.Path("/player_decisions").Methods("POST").HandlerFunc(admin.handlePlayerDecisionsEvent)
	r.Path("/test_command").Methods("POST")
	utils.RoutesSummary(r, admin.logger)
	return r
}

func (admin *Admin) Restart() {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	admin.table = createStartingTable(&envConfig)

	admin.logger = uknow.CreateFileLogger(false, logFilePrefix)

	admin.listenAddrOfPlayer = make(map[string]utils.TCPAddress)
	admin.shuffler = ""
	admin.state = AddingPlayers
	admin.expectedAcksList = newExpectedAcksState(admin.logger)

	log.Print("Admin restarted...")
}

func (admin *Admin) RunServer() {
	admin.logger.Printf("Running admin server at addr: %s", admin.httpServer.Addr)
	go admin.expectedAcksList.waitForAcks()
	err := admin.httpServer.ListenAndServe()

	admin.updatePromptWithStateInfo()
	if err != nil {
		log.Fatalf("Admin.RunServer() failed: %s", err.Error())
	}
}

const allPlayersSyncCommandTimeout = time.Duration(10) * time.Second

// Req:		POST /player AddNewPlayerMessage
// Resp:	AddNewPlayerMessage
func (admin *Admin) handleAddNewPlayer(w http.ResponseWriter, r *http.Request) {
	admin.logger.Printf("addNewPlayer receeived from %s", r.URL.Host)

	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	if admin.state != AddingPlayers {
		fmt.Fprintf(w, "Not accepting new players, currently in state: %s", admin.state)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// Parse message
	var msg messages.AddNewPlayersMessage

	err := messages.DecryptAndDecodeJSON(&msg, r.Body, admin.aesCipher)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if len(msg.ClientListenAddrs) != 1 || len(msg.PlayerNames) != 1 {
		admin.logger.Print("Bad request. Must have exactly 1 player and the listen address in AddNewPlayerMessage")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	newPlayerName := msg.PlayerNames[0]
	newPlayerListenAddr := msg.ClientListenAddrs[0]

	// Tell existing players about the new player
	admin.logger.Printf("newPlayerName = %s, newPlayerHost = %s, newPlayerPort = %d", newPlayerName, newPlayerListenAddr.Host, newPlayerListenAddr.Port)

	// Add the player to the local table. **But don't if it's already added
	// by hand-reader - in which case check that we have this player in the
	// table module.**
	if admin.table.IsShuffled {
		_, ok := admin.table.HandOfPlayer[newPlayerName]
		if !ok {
			admin.logger.Printf("player %s has not been loaded by hand-reader. see the JSON config.", newPlayerName)
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
	} else {
		err = admin.table.AddPlayer(newPlayerName)
		if errors.Is(err, uknow.ErrPlayerAlreadyExists) {
			w.WriteHeader(http.StatusOK)
			admin.logger.Printf("player %s already exists", newPlayerName)
			return
		}

		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			admin.logger.Printf("Cannot add new player: %s", err)
			return
		}
	}

	// Add the player's listen address
	admin.listenAddrOfPlayer[newPlayerName] = newPlayerListenAddr
	// Set it as shuffler, although it doesn't matter
	admin.shuffler = newPlayerName

	// Tell existing players about new player asynchronously
	go admin.tellExistingPlayersAboutNew(context.Background(), newPlayerName, newPlayerListenAddr.Host, newPlayerListenAddr.Port)

	// Tell the new player about existing players. This is by sending AddNewPlayersMessage as a response containing the existing players info.
	var respAddNewPlayersMessage messages.AddNewPlayersMessage
	for playerName, playerListenAddr := range admin.listenAddrOfPlayer {
		if playerName == newPlayerName {
			continue
		}

		admin.logger.Printf("Telling existing player '%s' about '%s'", playerName, newPlayerName)
		addr, err := utils.ResolveTCPAddress(playerListenAddr.String())
		if err != nil {
			admin.logger.Printf("Failed to resolve playerListenAddr. %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			messages.WriteErrorPayload(w, err)
			continue
		}
		respAddNewPlayersMessage.Add(playerName, addr.Host, addr.Port, "http")

		// Also add an expecting-ack that admin should receive from the new player for connecting to each of the existing players.

		admin.expectedAcksList.addPending(expectedAck{ackerPlayerName: newPlayerName, ackId: makeAckIdConnectedPlayer(newPlayerName, playerName)}, 10*time.Second, func() {}, func() {
			admin.logger.Printf("Ack timeout: new player %s could not connect to existing player %s in time", newPlayerName, playerName)
		})
	}

	w.WriteHeader(http.StatusOK)
	messages.EncodeJSONAndEncrypt(&respAddNewPlayersMessage, w, admin.aesCipher)
}

func (admin *Admin) tellExistingPlayersAboutNew(ctx context.Context, newPlayerName, newPlayerHost string, newPlayerPort int) {
	// Message is same for all players. Create it.
	var addPlayerMsg messages.AddNewPlayersMessage
	addPlayerMsg.Add(newPlayerName, newPlayerHost, newPlayerPort, "http")

	ctxForAllRequests, cancelFunc := context.WithTimeout(ctx, allPlayersSyncCommandTimeout)
	defer cancelFunc()
	g, ctx := errgroup.WithContext(ctxForAllRequests)

	for playerName, playerListenAddr := range admin.listenAddrOfPlayer {
		if playerName == newPlayerName {
			continue
		}

		playerListenAddr := playerListenAddr
		playerName := playerName

		g.Go(func() error {
			url := playerListenAddr.HTTPAddress() + "/players"
			admin.logger.Printf("telling existing player %s at url %s about new player %s at url %s", playerName, playerListenAddr.String(), newPlayerName, url)

			var b bytes.Buffer
			messages.EncodeJSONAndEncrypt(&addPlayerMsg, &b, admin.aesCipher)

			requestSender := utils.RequestSender{
				Client:     admin.httpClient,
				Method:     "POST",
				URL:        url,
				BodyReader: &b,
			}

			resp, err := requestSender.Send(ctx)

			if err != nil {
				return err
			}

			if resp.StatusCode != http.StatusOK {
				admin.logger.Printf("response from existing player %s on /player: %s", playerName, resp.Status)
				return fmt.Errorf("failed to call POST /players on player %s", playerName)
			}

			// Add an expecting-ack for this existing player. The existing player will send an ack asynchronously denoting that it has established connection with the new player
			admin.expectedAcksList.addPending(
				expectedAck{
					ackId:           makeAckIdConnectedPlayer(playerName, newPlayerName),
					ackerPlayerName: playerName,
				},
				5*time.Second,
				func() {},
				func() {
					admin.logger.Printf("ack timeout: existing player %s could not connect to new player %s in time", playerName, newPlayerName)
				},
			)

			admin.logger.Printf("Done telling existing player %s about new player %s at url %s, awaiting ack", playerName, newPlayerName, url)

			return nil
		})
	}

	err := g.Wait()

	if err != nil {
		admin.logger.Printf("Failed to add new player to one or more other players: %s", err)
	}

	admin.logger.Printf("listenAddrOfPlayer: %+v", admin.listenAddrOfPlayer)

}

func (admin *Admin) handleAckNewPlayerAdded(w http.ResponseWriter, r *http.Request) {
	admin.logger.Println("handleAckNewPlayerAdded called")

	var reqBody messages.AckNewPlayerAddedMessage
	err := messages.DecryptAndDecodeJSON(&reqBody, r.Body, admin.aesCipher)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		messages.WriteErrorPayload(w, err)
		return
	}

	// Find and remove from this ack from the expectingAcks list. Don't forget to lock it first.
	ack := expectedAck{
		ackId:           makeAckIdConnectedPlayer(reqBody.AckerPlayer, reqBody.NewPlayer),
		ackerPlayerName: reqBody.AckerPlayer,
	}

	admin.logger.Printf("ack: %+v", ack)

	admin.expectedAcksList.chNewAckReceived <- ack
	w.WriteHeader(http.StatusOK)
}

// Req: POST /set_ready SetReadyMessage
//
// Resp: StatusForbidden
// Resp: SeeOther, UnwrappedErrorPayload
func (admin *Admin) handleSetReady(w http.ResponseWriter, r *http.Request) {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	senderAddr, err := utils.ResolveTCPAddress(r.RemoteAddr)
	if err != nil {
		admin.logger.Printf("%s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	senderPlayerName, err := playerWithAddress(senderAddr, admin.listenAddrOfPlayer)
	if err != nil {
		admin.logger.Printf("%s", err)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if admin.setReadyPlayer != "" && (senderPlayerName != admin.setReadyPlayer) {
		admin.logger.Printf("player %s not set as setReadyPlayer", senderPlayerName)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	admin.logger.Printf("handleSetReady: called from address: %s", senderAddr.BindString())

	if admin.state != AddingPlayers {
		w.WriteHeader(http.StatusForbidden)
		admin.logger.Printf("Expecting admin state: %s, but have %s", AddingPlayers, admin.state)
		errorResponse := messages.UnwrappedErrorPayload{}
		errorResponse.Add(fmt.Errorf("handleSetReady: Failed due to %w", errorInvalidAdminState))

		messages.EncodeJSONAndEncrypt(&errorResponse, w, admin.aesCipher)
		return
	}

	admin.expectedAcksList.mu.Lock()
	numExpectingAcks := len(admin.expectedAcksList.pendingAcks)

	if numExpectingAcks != 0 {
		admin.expectedAcksList.mu.Unlock()
		admin.logger.Printf("handleSetReady: cannot change to ready state, numExpectingAcks = %d (!= 0)", numExpectingAcks)

		w.WriteHeader(http.StatusSeeOther)

		errorResponse := messages.UnwrappedErrorPayload{}
		errorResponse.Add(fmt.Errorf("handleSetReady: %w, numExpectingAcks: %d", errorWaitingForAcks, numExpectingAcks))

		messages.EncodeJSONAndEncrypt(&errorResponse, w, admin.aesCipher)
		return
	}

	admin.expectedAcksList.mu.Unlock()

	var setReadyMessage messages.SetReadyMessage
	err = messages.DecryptAndDecodeJSON(&setReadyMessage, r.Body, admin.aesCipher)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		messages.WriteErrorPayload(w, err)
		return
	}

	admin.setState(ReadyToServeCards)

	// shuffle and serve cards. then sync the table state with each player.
	admin.table.ShufflerName = setReadyMessage.ShufflerName

	if !admin.table.IsShuffled {
		admin.table.ShuffleDeckAndDistribute(8)
	}

	if setReadyMessage.ShufflerIsFirstPlayer {
		admin.table.PlayerOfNextTurn = admin.table.ShufflerName
		admin.table.PlayerOfLastTurn = admin.table.ShufflerName
	}

	go admin.sendServeCardsEventToAllPlayers()

	w.WriteHeader(http.StatusOK)
}

// Req: POST /player_decisions_event
func (admin *Admin) handlePlayerDecisionsEvent(w http.ResponseWriter, r *http.Request) {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	switch admin.state {
	case WaitingForPlayerDecision:
		var event messages.PlayerDecisionsEvent
		err := messages.DecryptAndDecodeJSON(&event, r.Body, admin.aesCipher)
		if err != nil {
			admin.logger.Printf("handlePlayerDecisionsEvent: %s", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		admin.logger.Printf("Received decisions event from player: %s, decisions: %+v, decisionCounter: %d", event.DecidingPlayer, event.Decisions, event.DecisionEventCounter)

		ack := expectedAck{
			ackId:           makeAckIdWaitingForPlayerDecision(event.DecidingPlayer, event.DecisionEventCounter),
			ackerPlayerName: event.DecidingPlayer,
		}

		if event.DecisionEventCounter != admin.decisionEventsCompleted {
			admin.logger.Printf("Unexpected decision event counter in ack: %s, but admin decision counter is %d", ack.ackId, admin.decisionEventsCompleted)
		}

		admin.expectedAcksList.chNewAckReceived <- ack

		go admin.syncPlayerDecisionsEvent(event)

	default:
		// DTL(@rk): What happens when the waiting player disconnects? I think anytime we stop receiving heartbeats, we should reset the admin and notify the clients that the admin is resetting.
		if admin.state != WaitingForPlayerDecision {
			w.WriteHeader(http.StatusSeeOther)
			err := fmt.Errorf("%w: %s", errorInvalidAdminState, admin.state)
			admin.logger.Printf("handlePlayerDecisionsEvent: %s", err.Error())
			errorResponse := messages.UnwrappedErrorPayload{}
			errorResponse.Add(err)
			messages.EncodeJSONAndEncrypt(&errorResponse, w, admin.aesCipher)
			return
		}
	}
}

func (admin *Admin) syncPlayerDecisionsEvent(event messages.PlayerDecisionsEvent) {
	syncEvent := &messages.PlayerDecisionsSyncEvent{
		PlayerDecisionsEvent: event,
	}

	// Evaluate the decisions on the admin table
	admin.stateMutex.Lock()
	admin.table.EvalPlayerDecisionsNoTransferChan(syncEvent.DecidingPlayer, syncEvent.Decisions)
	admin.setState(SyncingPlayerDecision)
	admin.updatePromptWithStateInfo()
	admin.stateMutex.Unlock()

	err := admin.sendMessageToAllPlayers(context.TODO(), syncEvent.RestPath(), &syncEvent)
	if err != nil {
		// TODO(@rk): Don't handle for now. Happy path only
		admin.logger.Printf("Failed to broadcast player decisions event: %s", err)
		return
	}

	admin.stateMutex.Lock()
	admin.decisionEventsCompleted++
	admin.setState(DoneSyncingPlayerDecision)
	admin.stateMutex.Unlock()

	// already inside a goroutine, so we don't need to spawn a new one for runNewTurn()
	admin.runNewTurn()
}

func (admin *Admin) runNewTurn() {
	if envConfig.DebugNewTurnViaPrompt {
		log.Printf("Waiting for `newturn` command before starting turn")
		return
	}

	<-time.After(time.Duration(envConfig.PauseMsecsBeforeNewTurn) * time.Millisecond)

	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	if admin.table.TableState == uknow.HaveWinner {
		log.Printf("Have winner: %s", admin.table.WinnerPlayerName)
		admin.setState(HaveWinner)
	} else {
		admin.logger.Printf("Starting new turn...")

		if admin.state != DoneSyncingPlayerDecision {
			admin.logger.Fatalf("startNewTurn: Unexpected state: %s", admin.state)
		}

		admin.setState(PlayerChosenForTurn)
		go admin.sendChosenPlayerEventToAllPlayers()
	}
}

// Call this to broadcast the event to all players. We can essentially send any
// []byte, but we make a decision to use this to only send event messages.
func (admin *Admin) sendMessageToAllPlayers(ctx context.Context, eventRestPath string, requestStruct interface{}) error {
	ctx, cancelFunc := context.WithTimeout(ctx, allPlayersSyncCommandTimeout)
	g, ctx := errgroup.WithContext(ctx)
	defer cancelFunc()

	for playerName, playerAddr := range admin.listenAddrOfPlayer {
		playerName := playerName
		playerAddr := playerAddr

		// TODO(@rk): We're copying one bytes.Buffer to another
		// bytes.Buffer here, can we avoid? Also we need to create a new
		// bytes.Buffer every round of the loop since we're making
		// requests concurrently.
		var b bytes.Buffer
		err := messages.EncodeJSONAndEncrypt(&requestStruct, &b, admin.aesCipher)
		if err != nil {
			admin.logger.Fatal(err)
		}

		g.Go(func() error {
			return admin.sendMessageToPlayer(ctx, playerName, playerAddr, eventRestPath, &b)
		})
	}

	return g.Wait()
}

func (admin *Admin) sendMessageToPlayer(
	ctx context.Context,
	playerName string,
	playerAddr utils.TCPAddress,
	eventRestPath string,
	requestBodyReader io.Reader) error {

	playerURL := fmt.Sprintf("%s/event/%s", playerAddr.String(), eventRestPath)

	admin.logger.Printf("Sending to playerURL: %s", playerURL)

	req := utils.RequestSender{
		Client:     admin.httpClient,
		Method:     "POST",
		URL:        playerURL,
		BodyReader: requestBodyReader,
	}

	resp, err := req.Send(ctx)
	if err != nil {
		return NewSendEventMessageFailedError(playerName, eventRestPath, err)
	}

	if resp.StatusCode != http.StatusOK {
		return NewSendEventMessageFailedError(playerName, eventRestPath, NewHTTPResponseCodeError(resp.StatusCode))
	}

	return nil
}

func (admin *Admin) sendServeCardsEventToAllPlayers() {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	eventMsg := messages.ServedCardsEvent{Table: *admin.table}

	err := admin.sendMessageToAllPlayers(context.TODO(), eventMsg.RestPath(), &eventMsg)

	if err != nil {
		admin.logger.Printf("sendServeCardsEventToAllPlayers failed: %s", err)
		return
	}

	admin.logger.Printf("sendServeCardsEventToAllPlayers success")

	admin.setState(CardsServed)
	go admin.sendChosenPlayerEventToAllPlayers()
}

func (admin *Admin) sendChosenPlayerEventToAllPlayers() {
	admin.logger.Printf("Waiting %.0f seconds before sending chosen player event", pauseBeforeChoosingPlayer.Seconds())

	<-time.After(pauseBeforeChoosingPlayer)

	admin.logger.Printf("Next turn: %s", admin.table.PlayerOfNextTurn)

	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	chosenPlayer := admin.table.PlayerOfNextTurn

	eventMsg := messages.ChosenPlayerEvent{
		PlayerName:           chosenPlayer,
		DecisionEventCounter: admin.decisionEventsCompleted,
	}
	err := admin.sendMessageToAllPlayers(context.TODO(), eventMsg.RestPath(), &eventMsg)

	if err != nil {
		admin.logger.Printf("sendChosenPlayerEventToAllPlayers: %s", err)
		return
	}

	admin.logger.Printf("sendChosenPlayerEventToAllPlayers success")

	admin.setState(WaitingForPlayerDecision)

	admin.expectedAcksList.addPending(
		expectedAck{
			ackId:           makeAckIdWaitingForPlayerDecision(chosenPlayer, admin.decisionEventsCompleted),
			ackerPlayerName: chosenPlayer,
		},
		// 20*time.Second,
		1*time.Hour,
		func() {},
		func() {
			admin.logger.Printf("Ack timeout: Failed to receive player decision event from player %s", chosenPlayer)
		},
	)
}

func (admin *Admin) setReady() {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	if admin.state != AddingPlayers {
		admin.logger.Printf("Expecting admin state: %s, but have %s", AddingPlayers, admin.state)
	}

	admin.expectedAcksList.mu.Lock()
	numExpectingAcks := len(admin.expectedAcksList.pendingAcks)

	if numExpectingAcks != 0 {
		admin.expectedAcksList.mu.Unlock()
		admin.logger.Printf("handleSetReady: cannot change to ready state, numExpectingAcks = %d (!= 0)", numExpectingAcks)
	}

	admin.expectedAcksList.mu.Unlock()
}

func (admin *Admin) setState(state AdminState) {
	admin.state = state
	admin.updatePromptWithStateInfo()
}

func (admin *Admin) updatePromptWithStateInfo() {
	if admin.rl == nil {
		return
	}

	switch admin.state {
	case AddingPlayers:
		admin.rl.SetPrompt("[adding_players]> ")
	case ReadyToServeCards:
		admin.rl.SetPrompt("[ready_to_serve_cards]> ")
	case CardsServed:
		admin.rl.SetPrompt("[cards_served]> ")
	case PlayerChosenForTurn:
		admin.rl.SetPrompt(fmt.Sprintf("[player_chosen_for_turn:%s]> ", admin.table.PlayerOfNextTurn))
	case WaitingForPlayerDecision:
		admin.rl.SetPrompt(fmt.Sprintf("[waiting_for_player_decision:%s]> ", admin.table.PlayerOfNextTurn))
	case WaitingForChallengePlayerDecision:
		admin.rl.SetPrompt(fmt.Sprintf("[waiting_for_challenge_decision:%s]> ", admin.table.PlayerOfNextTurn))
	case SyncingPlayerDecision:
		admin.rl.SetPrompt(fmt.Sprintf("[syncing_player_decision:%s]> ", admin.table.PlayerOfNextTurn))
	case DoneSyncingPlayerDecision:
		admin.rl.SetPrompt(fmt.Sprintf("[done_syncing_player_decision:(decider:%s, next:%s)]> ", admin.table.PlayerOfLastTurn, admin.table.PlayerOfNextTurn))
	case HaveWinner:
		admin.rl.SetPrompt(fmt.Sprintf("[have_winner:%s]> ", admin.table.WinnerPlayerName))
	}

	admin.rl.Write([]byte("\n"))
}

func (admin *Admin) RunREPL() {
	var err error
	admin.rl, err = readline.New("> ")
	if err != nil {
		log.Fatal(err)
	}
	defer admin.rl.Close()

	for {
		line, err := admin.rl.Readline()
		if err != nil {
			break
		}

		if line == "q" || line == "quit" {
			return
		}

		if line == "restart" || line == "re" {
			admin.Restart()
			continue
		}

		if line == "acks" {
			log.Printf("Expecting acks:\n%s", admin.expectedAcksList.ackIds())
			continue
		}

		if line == "state" {
			admin.stateMutex.Lock()
			log.Printf("%s", admin.state)
			admin.stateMutex.Unlock()
			continue
		}

		if line == "table-summary" {
			admin.stateMutex.Lock()
			log.Print(admin.table.Summary())
			admin.stateMutex.Unlock()
			continue
		}

		if line == "set_ready" || line == "sr" {
			admin.setReady()
		}
	}
}

// If there's a starting hand-config specified for debugging, we create a table accordingly
func createStartingTable(c *EnvConfig) *uknow.Table {
	tableLogger := uknow.CreateFileLogger(false, "table_admin")
	table := uknow.NewAdminTable(tableLogger)
	var err error

	if c.DebugStartingHandConfigFile != "" {
		table, err = hand_reader.LoadConfigFromFile(c.DebugStartingHandConfigFile, table, tableLogger)
	} else if c.DebugStartingHandConfigJSON != "" {
		table, err = hand_reader.LoadConfig([]byte(c.DebugStartingHandConfigJSON), table, log.Default())
	} else {
		return table
	}

	if err != nil {
		log.Fatalf("failed to load hand-config: %s", err)
	} else {
		log.Printf("loaded hand-config")
	}
	return table
}

func RunApp() {
	var adminConfigFile string
	flag.StringVar(&adminConfigFile, "conf", ".env", "Dotenv config file for admin server")

	flag.Parse()

	if adminConfigFile == ".env" {
		log.Print("No config file given, reading from .env")
	}

	err := godotenv.Load(adminConfigFile)
	if err != nil {
		log.Fatal(err.Error())
	}

	err = envconfig.Process("ADMIN", &envConfig)
	if err != nil {
		log.Fatal(err.Error())
	}

	commonConfig, err := cmdcommon.LoadCommonConfig()
	if err != nil {
		log.Fatalf("failed to load common config: %v", err)
	}

	var aesCipher *uknow.AESCipher

	if commonConfig.EncryptMessages {
		aesCipher, err = uknow.NewAESCipher(commonConfig.AESKey)
		if err != nil {
			log.Fatalf("failed to create aes cipher: %v", err)
		}
	}

	config := &ConfigNewAdmin{}
	config.ListenAddr = utils.TCPAddress{Host: envConfig.ListenAddr, Port: envConfig.ListenPort}

	config.Table = createStartingTable(&envConfig)
	config.SetReadyPlayer = envConfig.SetReadyPlayer
	config.aesCipher = aesCipher

	admin := NewAdmin(config)

	// Admin REPL
	if envConfig.RunREPL {
		go admin.RunServer()
		admin.RunREPL()
	} else {
		admin.RunServer()
	}
}
