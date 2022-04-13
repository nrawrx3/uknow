package admin

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chzyer/readline"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/rksht/uknow"
	"github.com/rksht/uknow/internal/messages"
	"github.com/rksht/uknow/internal/utils"
	"golang.org/x/sync/errgroup"
)

func playerWithAddress(addr utils.TCPAddress, listenAddrOfPlayer map[string]utils.TCPAddress) (string, error) {
	for playerName, valAddr := range listenAddrOfPlayer {
		if valAddr.Host == addr.Host {
			return playerName, nil
		}
	}
	return "", fmt.Errorf("%w: no player with address %s", errorUnknownPlayer, addr.BindString())
}

// A struct containing info about acks being waited on by server. Note that ackId, and sourcePlayer, must fix the expectedAck entity
type expectedAck struct {
	ackId           string
	ackerPlayerName string
	deadline        time.Time
	afterAckFn      func()
}

func (ack *expectedAck) equal(ack1 *expectedAck) bool {
	return ack.ackId == ack1.ackId && ack.ackerPlayerName == ack1.ackerPlayerName
}

type expectedAcksState struct {
	mu               sync.Mutex
	list             []*expectedAck
	chNewAckReceived chan *expectedAck
}

func newExpectedAcksState() *expectedAcksState {
	return &expectedAcksState{
		list:             make([]*expectedAck, 0, 16),
		chNewAckReceived: make(chan *expectedAck),
	}
}

func (es *expectedAcksState) addAck(ack *expectedAck) {
	es.mu.Lock()
	log.Printf("Adding new expecting ack to list %+v", ack)
	es.list = append(es.list, ack)
	es.mu.Unlock()
}

func (es *expectedAcksState) ackIds() string {
	es.mu.Lock()
	defer es.mu.Unlock()
	var sb strings.Builder
	for _, ack := range es.list {
		sb.WriteString(ack.ackId)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (es *expectedAcksState) runLoop() {
	for expectedAck := range es.chNewAckReceived {
		es.mu.Lock()

		for i, ack := range es.list {
			if !ack.equal(expectedAck) {
				continue
			}

			log.Printf("Acking the ack: %s", ack.ackId)

			ack.afterAckFn()
			es.list = append(es.list[0:i], es.list[i+1:len(es.list)]...)
			break
		}

		es.mu.Unlock()
	}
}

func makeAckIdConnectedPlayer(ackerPlayer, connectedPlayer string) string {
	return fmt.Sprintf("%s_connected_to_%s", ackerPlayer, connectedPlayer)
}

type AdminState string

const (
	AddingPlayers             AdminState = "adding_players"
	ReadyToServeCards         AdminState = "ready_to_serve_cards"
	CardsServed               AdminState = "cards_served"
	PlayerChosenForTurn       AdminState = "player_chosen"
	WaitingForPlayerDecision  AdminState = "waiting_for_player_decision"
	SyncingPlayerDecision     AdminState = "syncing_player_decision"
	DoneSyncingPlayerDecision AdminState = "done_syncing_player_decision"
)

const countdownBeforeChoosingPlayer = 2 * time.Second

type Admin struct {
	table      *uknow.Table
	stateMutex sync.Mutex
	state      AdminState

	// Address of player registered on connect command
	listenAddrOfPlayer map[string]utils.TCPAddress
	shuffler           string
	setReadyPlayer     string
	httpClient         *http.Client
	httpServer         *http.Server
	logger             *log.Logger

	expectedAcksState *expectedAcksState
}

type ConfigNewAdmin struct {
	ListenAddr     utils.TCPAddress
	Table          *uknow.Table
	SetReadyPlayer string
}

const logFilePrefix = "uknow_admin"

func NewAdmin(config *ConfigNewAdmin) *Admin {
	admin := &Admin{
		table:              uknow.NewAdminTable(),
		listenAddrOfPlayer: make(map[string]utils.TCPAddress),
		shuffler:           "",
		state:              AddingPlayers,
		expectedAcksState:  newExpectedAcksState(),
		logger:             utils.CreateFileLogger(false, logFilePrefix),
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

func (a *Admin) Restart() {
	a.stateMutex.Lock()
	defer a.stateMutex.Unlock()

	a.table = uknow.NewAdminTable()

	a.logger = utils.CreateFileLogger(false, logFilePrefix)

	a.listenAddrOfPlayer = make(map[string]utils.TCPAddress)
	a.shuffler = ""
	a.state = AddingPlayers
	a.expectedAcksState = newExpectedAcksState()

	log.Print("Admin restarted...")
}

func (admin *Admin) RunServer() {
	admin.logger.Printf("Running admin server at addr: %s", admin.httpServer.Addr)
	go admin.expectedAcksState.runLoop()
	err := admin.httpServer.ListenAndServe()
	if err != nil {
		log.Fatalf("Admin.RunServer() failed: %s", err.Error())
	}
}

const allPlayersSyncCommandTimeout = time.Duration(10) * time.Second

var errorWaitingForAcks = errors.New("waiting for acks")
var errorInvalidAdminState = errors.New("invalid admin state")
var errorUnknownPlayer = errors.New("unknown player")
var errorHttpResponseFromClient = errors.New("error http response from client")

// Req:		POST /player AddNewPlayerMessage
// Resp:	AddNewPlayerMessage
func (admin *Admin) handleAddNewPlayer(w http.ResponseWriter, r *http.Request) {
	log.Print("addNewPlayer...")
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

	err := json.NewDecoder(r.Body).Decode(&msg)
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

	// Add the player to the local table
	err = admin.table.AddPlayer(newPlayerName)
	if errors.Is(err, uknow.PlayerAlreadyExists) {
		w.WriteHeader(http.StatusOK)
		admin.logger.Printf("player %s already exists", newPlayerName)
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		admin.logger.Printf("Cannot add new player: %s", err)
		return
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
		admin.expectedAcksState.addAck(&expectedAck{
			ackerPlayerName: newPlayerName,
			ackId:           makeAckIdConnectedPlayer(newPlayerName, playerName),
			deadline:        time.Now().Add(10 * time.Second),
			afterAckFn: func() {
				admin.logger.Printf("ack received: %s (new player) connected to %s (existing)", newPlayerName, playerName)
			},
		})
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(respAddNewPlayersMessage)
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
			admin.logger.Printf("Telling existing player %s at url %s about new player %s at url %s", playerName, playerListenAddr.String(), newPlayerName, url)

			requestSender := utils.RequestSender{
				Client:     admin.httpClient,
				Method:     "POST",
				URL:        url,
				BodyReader: messages.MustJSONReader(&addPlayerMsg),
			}

			resp, err := requestSender.Send(ctx)

			if err != nil {
				return err
			}

			if resp.StatusCode != http.StatusOK {
				admin.logger.Printf("Response from existing player %s on /player: %s", playerName, resp.Status)
				return fmt.Errorf("failed to call POST /players on player %s", playerName)
			}

			// Add an expecting-ack for this existing player. The existing player will send an ack asynchronously denoting that it has established connection with the new player
			admin.expectedAcksState.addAck(&expectedAck{
				ackId:           makeAckIdConnectedPlayer(playerName, newPlayerName),
				ackerPlayerName: playerName,
				afterAckFn: func() {
					admin.logger.Printf("ack received: existing player %s connected to new player %s", playerName, newPlayerName)
				},
			})

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
	err := json.NewDecoder(r.Body).Decode(&reqBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		messages.WriteErrorPayload(w, err)
		return
	}

	// Find and remove from this ack from the expectingAcks list. Don't forget to lock it first.
	ack := &expectedAck{
		ackId:           makeAckIdConnectedPlayer(reqBody.AckerPlayer, reqBody.NewPlayer),
		ackerPlayerName: reqBody.AckerPlayer,
	}

	admin.logger.Printf("ack: %+v", ack)

	admin.expectedAcksState.chNewAckReceived <- ack
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
		json.NewEncoder(w).Encode(errorResponse)
		return
	}

	admin.expectedAcksState.mu.Lock()
	numExpectingAcks := len(admin.expectedAcksState.list)

	if numExpectingAcks != 0 {
		admin.expectedAcksState.mu.Unlock()
		admin.logger.Printf("handleSetReady: cannot change to ready state, numExpectingAcks = %d (!= 0)", numExpectingAcks)

		w.WriteHeader(http.StatusSeeOther)

		errorResponse := messages.UnwrappedErrorPayload{}
		errorResponse.Add(fmt.Errorf("handleSetReady: %w, numExpectingAcks: %d", errorWaitingForAcks, numExpectingAcks))
		json.NewEncoder(w).Encode(errorResponse)
		return
	}

	admin.expectedAcksState.mu.Unlock()

	var setReadyMessage messages.SetReadyMessage
	err = json.NewDecoder(r.Body).Decode(&setReadyMessage)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		messages.WriteErrorPayload(w, err)
		return
	}

	admin.state = ReadyToServeCards

	// shuffle and serve cards. then sync the table state with each player.
	admin.table.ShufflerName = setReadyMessage.ShufflerName
	admin.table.ShuffleDeckAndDistribute(8)

	if setReadyMessage.ShufflerIsFirstPlayer {
		admin.table.NextPlayerToDraw = admin.table.ShufflerName
	}

	go admin.sendServeCardsEventToAllPlayers()

	w.WriteHeader(http.StatusOK)
}

// Req: POST /player_decisions_event
func (admin *Admin) handlePlayerDecisionsEvent(w http.ResponseWriter, r *http.Request) {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	// DTL(@rk): What happens when the waiting player disconnects? I think anytime we stop receiving heartbeats, we should reset the admin and notify the clients that the admin is resetting.
	if admin.state != WaitingForPlayerDecision {
		w.WriteHeader(http.StatusSeeOther)
		err := fmt.Errorf("%w: %s", errorInvalidAdminState, admin.state)
		admin.logger.Printf("handlePlayerDecisionsEvent: %s", err.Error())
		errorResponse := messages.UnwrappedErrorPayload{}
		errorResponse.Add(err)
		json.NewEncoder(w).Encode(errorResponse)
		return
	}

	var event messages.PlayerDecisionsEvent
	err := json.NewDecoder(r.Body).Decode(&event)
	if err != nil {
		admin.logger.Printf("handlePlayerDecisionsEvent: %s", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	admin.logger.Printf("Received decisions event from player: %s", event.PlayerName)

	go admin.syncPlayerDecisionListEvent(event)
}

func (admin *Admin) syncPlayerDecisionListEvent(event messages.PlayerDecisionsEvent) {
	// FILTHY(@rk): Just spawning this goroutine to consume the card transfer events. The admin does not have a UI, so we could simply log it. Have to think.
	transferEventsChan := make(chan uknow.CardTransferEvent)
	go func() {
		for transferEvent := range transferEventsChan {
			admin.logger.Printf("Received %s", transferEvent.String())
		}
	}()

	syncEvent := &messages.PlayerDecisionsSyncEvent{
		PlayerDecisionsEvent: event,
	}

	admin.stateMutex.Lock()
	admin.table.EvalPlayerDecisions(syncEvent.PlayerName, syncEvent.Decisions, transferEventsChan)
	admin.state = SyncingPlayerDecision
	admin.stateMutex.Unlock()

	err := admin.sendMessageToAllPlayers(context.TODO(), syncEvent.RestPath(), &syncEvent)
	if err != nil {
		// TODO(@rk): Don't handle for now. Happy path only
		admin.logger.Printf("Failed to broadcast player decisions event: %s", err)
		return
	}

	admin.stateMutex.Lock()
	admin.state = DoneSyncingPlayerDecision
	admin.stateMutex.Unlock()
}

// Call this to broadcast the event too all players. We can essentially send any []byte, but we make a decision to use this to only send event messages.
func (admin *Admin) sendMessageToAllPlayers(ctx context.Context, eventRestPath string, requestStruct interface{}) error {
	ctx, _ = context.WithTimeout(ctx, allPlayersSyncCommandTimeout)
	g, ctx := errgroup.WithContext(ctx)

	for playerName, playerAddr := range admin.listenAddrOfPlayer {
		playerName := playerName
		playerAddr := playerAddr

		g.Go(func() error {
			return admin.sendMessageToPlayer(ctx, playerName, playerAddr, eventRestPath, messages.MustJSONReader(requestStruct))
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

	admin.state = CardsServed

	go admin.sendChosenPlayerEventToAllPlayers()
}

func (admin *Admin) sendChosenPlayerEventToAllPlayers() {
	admin.logger.Printf("Waiting %.0f seconds before sending chosen player event", countdownBeforeChoosingPlayer.Seconds())

	<-time.After(countdownBeforeChoosingPlayer)

	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	eventMsg := messages.ChosenPlayerEvent{PlayerName: admin.table.NextPlayerToDraw}
	err := admin.sendMessageToAllPlayers(context.TODO(), eventMsg.RestPath(), &eventMsg)

	if err != nil {
		admin.logger.Printf("sendChosenPlayerEventToAllPlayers: %s", err)
		return
	}

	admin.logger.Printf("sendChosenPlayerEventToAllPlayers success")

	admin.state = WaitingForPlayerDecision
}

func (admin *Admin) setReady() {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	if admin.state != AddingPlayers {
		admin.logger.Printf("Expecting admin state: %s, but have %s", AddingPlayers, admin.state)
	}

	admin.expectedAcksState.mu.Lock()
	numExpectingAcks := len(admin.expectedAcksState.list)

	if numExpectingAcks != 0 {
		admin.expectedAcksState.mu.Unlock()
		admin.logger.Printf("handleSetReady: cannot change to ready state, numExpectingAcks = %d (!= 0)", numExpectingAcks)
	}

	admin.expectedAcksState.mu.Unlock()
}

type EnvConfig struct {
	ListenAddr     string `split_words:"true" required:"true"`
	ListenPort     int    `split_words:"true" required:"true"`
	RunREPL        bool   `split_words:"true" required:"false" default:"true"`
	SetReadyPlayer string `split_words:"true" required:"true"`
}

func (admin *Admin) RunREPL() {
	rl, err := readline.New("> ")
	if err != nil {
		log.Fatal(err)
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
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
			log.Printf("Expecting acks:\n%s", admin.expectedAcksState.ackIds())
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

func RunApp() {
	var envConfig EnvConfig
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

	config := &ConfigNewAdmin{}
	config.ListenAddr = utils.TCPAddress{Host: envConfig.ListenAddr, Port: envConfig.ListenPort}
	config.Table = uknow.NewAdminTable()
	config.SetReadyPlayer = envConfig.SetReadyPlayer

	admin := NewAdmin(config)

	// Admin REPL
	if envConfig.RunREPL {
		go admin.RunServer()
		admin.RunREPL()
	} else {
		admin.RunServer()
	}
}
