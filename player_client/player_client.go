package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"github.com/rksht/uknow"
	messages "github.com/rksht/uknow/internal/messages"
	utils "github.com/rksht/uknow/internal/utils"
	"golang.org/x/sync/errgroup"
)

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type ClusterMap map[string]utils.TCPAddress // Map of player name to their public address

type PlayerClientState string

const (
	WaitingToConnectToAdmin       PlayerClientState = "waiting_to_connect_to_admin"
	WaitingForAdminToServeCards   PlayerClientState = "waiting_for_admin_to_serve_cards"
	WaitingForAdminToChoosePlayer PlayerClientState = "waiting_for_admin_to_choose_player"
	AskingUserForDecision         PlayerClientState = "asking_user_for_decision"
	WaitingForDecisionSync        PlayerClientState = "waiting_for_decision_sync"
)

// func (c ClusterMap) Clone(excludePlayers []string) ClusterMap {
// 	if excludePlayers == nil {
// 		excludePlayers = []string{}
// 	}

// 	cloned := make(ClusterMap)
// 	for k, v := range c {
// 		exclude := false
// 		for _, e := range excludePlayers {
// 			if e == k {
// 				exclude = true
// 				break
// 			}
// 		}
// 		if !exclude {
// 			cloned[k] = v
// 		}
// 	}
// 	return cloned
// }

// type askUIForUserTurnArgs struct {
// 	// The PlayerClient itself will wait on this channel to receive the command input from user
// 	receive               chan<- uknow.Command
// 	timeout               time.Duration
// 	sender                string
// 	appQuitting           bool // Set by client to let UI know that the app is stopping
// 	expectingUserDecision bool // Set by client to make UI expect a user decision command
// }

var ErrorClientAwaitingApproval = errors.New("client awaiting approval of previous command")
var ErrorClientUnexpectedSender = errors.New("unexpected sender")
var ErrorFailedToConnectToNewPlayer = errors.New("failed to connect to new player")
var ErrorUIFailedToConsumeCommand = errors.New("ui failed to consume command")
var ErrorUnknownEvent = errors.New("unknown event")
var ErrorFailedToEvaluateReplCommand = errors.New("failed to evaluate repl command")

type ClientChannels struct {
	GeneralUICommandPushChan       chan<- UICommand
	AskUserForDecisionPushChan     chan<- *UICommandAskUserForDecision
	NonDecisionReplCommandPullChan <-chan *ReplCommand
	LogWindowPushChan              chan<- string
}
type PlayerClient struct {
	// Used to protect the non-gui state
	stateMutex  sync.Mutex
	clientState PlayerClientState

	table *uknow.Table

	// Server used to service requests made by admin and other players
	httpServer *http.Server

	// NOTE(@rk): We are NOT storing connections to any of the other players in a PlayerClient. We let http.Transport do that for us. We simply keep a map of player name to player address.
	// When the admin sends an AddNewPlayer message via POST /players to a PlayerClient, the client makes a GET /ping on the new player's address to confirm that it can connect. This also
	// creates a connection to this client in http.Transport used by the PlayerClient.
	httpClient         *http.Client
	neighborListenAddr ClusterMap
	adminAddr          utils.TCPAddress

	// Exposes the player API to the game admin.
	router *mux.Router

	ClientChannels

	Logger *log.Logger

	debugFlags DebugFlags
}

type ConfigNewPlayerClient struct {
	ClientChannels
	TestErrorChan chan<- error
	Table         *uknow.Table
	// HttpListenAddr             string
	ListenAddr       utils.TCPAddress
	DefaultAdminAddr utils.TCPAddress
}

func NewPlayerClient(config *ConfigNewPlayerClient, debugFlags DebugFlags) *PlayerClient {
	c := &PlayerClient{
		table:              config.Table,
		clientState:        WaitingToConnectToAdmin,
		httpClient:         utils.CreateHTTPClient(),
		neighborListenAddr: make(ClusterMap),
		ClientChannels:     config.ClientChannels,
		Logger:             utils.CreateFileLogger(false, config.Table.LocalPlayerName),
		debugFlags:         debugFlags,
		adminAddr:          config.DefaultAdminAddr,
	}

	c.router = mux.NewRouter()

	c.initRouterHandlers()

	c.httpServer = &http.Server{
		Addr:    config.ListenAddr.BindString(),
		Handler: c.router,
	}

	c.Logger.Printf("Addr bind string = %s", config.ListenAddr.BindString())
	c.Logger.Printf("Bind address = %s", c.httpServer.Addr)

	// FILTHY(@rk):TODO(@rk): Delete this, see type definition
	go (&dummyCardTransferEventConsumer{
		decisionEventPullChan: DummyCardTransferEventConsumerChan,
	}).RunConsumer(c.Logger)

	return c
}

func (c *PlayerClient) RunServer() {
	c.Logger.Printf("Servicing admin commands at %s", c.httpServer.Addr)
	err := c.httpServer.ListenAndServe()
	if err != nil {
		log.Fatalf("PlayerClient.RunServer() failed: %s", err.Error())
	}
}

// Meant to be running in its goroutine. Handles non-play or inspect related commands.
func (c *PlayerClient) RunGeneralCommandHandler() {
	c.Logger.Printf("%s - running default command handler", c.table.LocalPlayerName)

	ctx := context.Background()

	for cmd := range c.NonDecisionReplCommandPullChan {
		// Logging for now
		c.Logger.Printf("default cmd `%+v`", cmd)

		switch cmd.Kind {
		case CmdConnect:
			c.Logger.Printf("Received a connect command from UI...")

			// Building the message struct first since it doesn't depend on mutable state
			var adminAddr utils.TCPAddress
			var err error

			if adminAddrString, ok := cmd.ExtraData.(string); ok {
				adminAddr, err = utils.ResolveTCPAddress(adminAddrString)
				if err != nil {
					c.Logger.Print(err)
					c.LogWindowPushChan <- fmt.Sprint(err)
				}
			} else {
				adminAddr = c.adminAddr
			}

			var msg messages.AddNewPlayersMessage

			listenAddr, err := utils.ResolveTCPAddress(c.httpServer.Addr)
			if err != nil {
				c.Logger.Fatal(err)
			}

			msg.Add(c.table.LocalPlayerName, listenAddr.Host, listenAddr.Port, "http")

			// Lock and check if we have the correct state. Connect to admin if yes.
			c.stateMutex.Lock()
			if c.clientState != WaitingToConnectToAdmin {
				c.LogWindowPushChan <- fmt.Sprintf("Invalid command for current state: %s", c.clientState)
				c.stateMutex.Unlock()
				continue
			}
			c.Logger.Printf("Will sending listenAddr %+v to admin", listenAddr)
			c.connectToAdmin(ctx, msg, adminAddr)
			c.stateMutex.Unlock()

		case CmdDeclareReady:
			c.Logger.Printf("Received a declare ready command from UI...")

			url := fmt.Sprintf("%s/set_ready", c.adminAddr.String())

			setReadyMessage := messages.SetReadyMessage{
				ShufflerName:          c.table.LocalPlayerName,
				ShufflerIsFirstPlayer: false,
			}

			resp, err := c.httpClient.Post(url, "application/json", messages.MustJSONReader(&setReadyMessage))

			if err != nil {
				c.Logger.Print(err)
			}

			if resp.StatusCode != http.StatusOK {
				c.LogWindowPushChan <- "Failed to send declare ready message"
				c.Logger.Printf("Failed to send declare ready message, resp status code: %s", resp.Status)
			}

		case CmdShowHand:
			c.Logger.Printf("Received showhand command from UI...")
			// Just printing to event log window
			c.logToWindow(c.table.HandOfPlayer[c.table.LocalPlayerName].String())

		default:
			c.Logger.Printf("RunDefaultCommandHandler: Unhandled command %s", cmd.Kind)
		}
	}

	log.Print("Exit RunDefaultCommandHandler...")
}

// DOES NOT LOCK stateMutex. Connects to admin and updates state on success.
func (c *PlayerClient) connectToAdmin(ctx context.Context, msg messages.AddNewPlayersMessage, adminAddr utils.TCPAddress) {
	var body bytes.Buffer
	err := json.NewEncoder(&body).Encode(msg)
	if err != nil {
		c.Logger.Fatal(err)
	}

	// CONSIDER: use ctx along with httpClient.Do
	url := fmt.Sprintf("%s/player", adminAddr.String())
	resp, err := c.httpClient.Post(url, "application/json", &body)
	if err != nil {
		c.Logger.Printf("connectToAdmin error: %s", err.Error())
		return
	}

	switch resp.StatusCode {
	case http.StatusSeeOther:
		c.Logger.Printf("connectToAdmin: Local player is already present in admin's table")
	case http.StatusOK:
		var respMsg messages.AddNewPlayersMessage
		err := json.NewDecoder(resp.Body).Decode(&respMsg)
		if err != nil {
			c.Logger.Fatalf("connectToAdmin: Failed to read OK response message. %s", err)
		} else {
			c.Logger.Printf("Done connecting to admin")
			c.adminAddr = adminAddr
			c.Logger.Printf("Will connect to currently existing players: %+v", respMsg)

			c.clientState = WaitingForAdminToServeCards
			go c.connectToEachPlayer(ctx, respMsg.PlayerNames, respMsg.ClientListenAddrs)
		}
	default:
		c.Logger.Fatalf("connectToAdmin: Unexpected response: %s", resp.Status)
	}
}

// func (c *PlayerClient) printTableInfo(uiState *ClientUI) {
// 	handCounts := make(map[string]int)
// 	for playerName, hand := range c.table.HandOfPlayer {
// 		handCounts[playerName] = len(hand)
// 	}

// 	msg := fmt.Sprintf(`
// Players:	%+v,
// Hand counts:	%+v,
// DrawDeck count: %+v,
// DiscardPile count: %+v`,
// 		c.table.PlayerNames, handCounts, len(c.table.DrawDeck), len(c.table.Pile))
// 	uiState.appendEventLog(msg)
// 	c.Logger.Printf(msg)
// 	c.LogWindowPushChan <- msg
// }

func (c *PlayerClient) logToWindow(format string, args ...interface{}) {
	format = "Client: " + format
	message := fmt.Sprintf(format, args...)
	c.LogWindowPushChan <- message
	c.Logger.Print(message)
}

func (c *PlayerClient) initRouterHandlers() {
	c.router.Path("/ping").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "pong")
	})

	// NOTE(@rk): Event the message handlers and paths convention. Starts with "/event"

	c.router.Path("/event/served_cards").Methods("POST").HandlerFunc(c.handleServedCardEvent)
	c.router.Path("/event/chosen_player").Methods("POST").HandlerFunc(c.handleChosenPlayerEvent)
	c.router.Path("/event/player_decisions_sync").Methods("POST").HandlerFunc(c.handlePlayerDecisionsSyncEvent)

	c.router.Path("/players").Methods("POST").HandlerFunc(c.handleAddNewPlayers)

	c.router.Path("/players").Methods("GET").HandlerFunc(c.handleGetPlayers)

	c.router.Path("/state").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()
		w.Write([]byte(c.clientState))
	})

	utils.RoutesSummary(c.router, c.Logger)
}

func (c *PlayerClient) handleAddNewPlayers(w http.ResponseWriter, r *http.Request) {
	var msg messages.AddNewPlayersMessage

	err := json.NewDecoder(r.Body).Decode(&msg)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// Set up the connection to the new player without blocking the http response to this call,
	// otherwise we're going to have a deadlock with the other client.
	go func() {
		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()
		c.connectToEachPlayer(context.Background(), msg.PlayerNames, msg.ClientListenAddrs)
	}()

	// Respond with an OK but add to the map later. Admin will be expecting this response before
	// game start.
	w.WriteHeader(http.StatusOK)
}

func (c *PlayerClient) handleGetPlayers(w http.ResponseWriter, r *http.Request) {
	c.Logger.Printf("Received GET /players")
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	msg := messages.GetPlayersMessage{
		ListenAddrOfPlayer: c.neighborListenAddr,
	}

	if err := json.NewEncoder(w).Encode(&msg); err != nil {
		c.Logger.Printf("GET /players error: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	c.Logger.Printf("Received GET /players. Sending: %+v", msg)
}

func (c *PlayerClient) handleServedCardEvent(w http.ResponseWriter, r *http.Request) {
	c.Logger.Printf("Received POST /event/served_cards")

	var servedCardsEvent messages.ServedCardsEvent

	err := json.NewDecoder(r.Body).Decode(&servedCardsEvent)
	if err != nil {
		c.Logger.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		messages.WriteErrorPayload(w, err)
		return
	}

	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	if c.clientState != WaitingForAdminToServeCards {
		c.Logger.Printf("Unexpected admin event: %s", servedCardsEvent.RestPath())
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	servedCardsEvent.Table.LocalPlayerName = c.table.LocalPlayerName
	c.table.Set(&servedCardsEvent.Table)

	uiCommand := &UICommandSetServedCards{
		table: &servedCardsEvent.Table,
	}

	err = c.sendCommandToUI(uiCommand, 2*time.Second)
	if err != nil {
		c.Logger.Print(err)
	}

	c.clientState = WaitingForAdminToChoosePlayer
}

func (c *PlayerClient) handleChosenPlayerEvent(w http.ResponseWriter, r *http.Request) {
	c.Logger.Printf("Received POST /event/served_cards")

	var chosenPlayerEvent messages.ChosenPlayerEvent
	err := json.NewDecoder(r.Body).Decode(&chosenPlayerEvent)
	if err != nil {
		c.Logger.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		messages.WriteErrorPayload(w, err)
		return
	}

	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	if c.clientState != WaitingForAdminToChoosePlayer {
		c.Logger.Printf("Unexpected admin event: %s", chosenPlayerEvent.RestPath())
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if c.table.LocalPlayerName == chosenPlayerEvent.PlayerName {
		c.logToWindow("It's our turn now!")
	} else {
		c.logToWindow("It's player %s's turn", chosenPlayerEvent.PlayerName)
		// TODO(@rk): Move to "waiting for player decision sync message from admin"
	}

	// TODO(@rk): next state
	c.clientState = AskingUserForDecision
	go c.askAndRunUserDecisions()
}

func (c *PlayerClient) askAndRunUserDecisions() {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	c.logToWindow("Asking for user decision")
	c.logToWindow(c.table.Summary())

	receiveReplCommandsChan := make(chan *ReplCommand)
	allowOneMoreDecision := make(chan bool)

	askCommand := &UICommandAskUserForDecision{
		receive:              receiveReplCommandsChan,
		allowOneMoreDecision: allowOneMoreDecision,
		timeout:              10 * time.Second, // TODO(@rk): Unused and arbitrary. Think later.
		sender:               "PlayerClient",   // TODO(@rk): Unused and arbitrary. Just delete.
	}

	c.AskUserForDecisionPushChan <- askCommand

	// Now consume the PlayerDecisionEvent(s) and send these to admin

	decisions := make([]uknow.PlayerDecision, 0, 4)

	for replCommand := range receiveReplCommandsChan {
		decision, err := c.EvalReplCommandOnTable(replCommand)

		if err != nil {
			c.Logger.Print(err)
			break
		}

		c.Logger.Printf("Received replCommand: %s, decisionEvent: %s", replCommand.Kind.String(), &decision)

		decisions = append(decisions, decision)

		// TODO(@rk): Uncomment. After proper game logic is implemented.
		// allowOneMoreDecision <- c.table.NextPlayerToDraw == c.table.LocalPlayerName

		// TODO(@rk): Remove. See above.
		allowOneMoreDecision <- false
	}

	c.Logger.Printf("Done receiving player decision events from ClientUI")

	// TODO(@rk): Send the PlayerDecisionEvent list as a request to admin on the /player_decisions path
	requestBody := messages.PlayerDecisionsEvent{
		Decisions:  decisions,
		PlayerName: c.table.LocalPlayerName,
	}

	requester := utils.RequestSender{
		URL:        fmt.Sprintf("%s/%s", c.adminAddr.String(), requestBody.RestPath()),
		Method:     "POST",
		Client:     c.httpClient,
		BodyReader: messages.MustJSONReader(&requestBody),
	}

	resp, err := requester.Send(context.TODO())
	if err != nil {
		err := fmt.Errorf("askAndRunUserDecisions: %w", err)
		c.Logger.Print(err)
		c.logToWindow(err.Error())
		return
	}
	if resp.StatusCode != http.StatusOK {
		c.Logger.Printf("askAndRunUserDecisions: Received status code %d from admin", resp.StatusCode)
		return
	}

	// TODO(@rk): Is WaitingForAdminToChoosePlayer correct to be the next state?
	c.Logger.Printf("Going to state %s", WaitingForAdminToChoosePlayer)
	c.clientState = WaitingForAdminToChoosePlayer
}

// POST /player_decisions
func (c *PlayerClient) handlePlayerDecisionsSyncEvent(w http.ResponseWriter, r *http.Request) {
	c.Logger.Printf("Received player_decisions_sync message")
	var decisionsEvent messages.PlayerDecisionsSyncEvent
	err := json.NewDecoder(r.Body).Decode(&decisionsEvent)
	if err != nil {
		c.Logger.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		messages.WriteErrorPayload(w, err)
		return
	}

	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	// Admin sends the sync event to all clients, including the client who generated the decisions. Handling this.
	if decisionsEvent.PlayerName == c.table.LocalPlayerName {
		return
	}

	if c.clientState != WaitingForDecisionSync {
		c.Logger.Printf("Unexpected event %s received while in state %s", decisionsEvent.RestPath(), c.clientState)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	c.table.EvalPlayerDecisions(decisionsEvent.PlayerName, decisionsEvent.Decisions, DummyCardTransferEventConsumerChan)

	c.clientState = WaitingForAdminToChoosePlayer
}

func (c *PlayerClient) EvalReplCommandOnTable(replCommand *ReplCommand) (uknow.PlayerDecision, error) {
	switch replCommand.Kind {
	case CmdDropCard:
		decisionEvent := uknow.PlayerDecision{
			Kind:       uknow.PlayerDecisionPlayHandCard,
			ResultCard: replCommand.Cards[0],
		}
		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decisionEvent, DummyCardTransferEventConsumerChan), nil

	case CmdDrawCard:
		decisionEvent := uknow.PlayerDecision{
			Kind: uknow.PlayerDecisionPullFromDeck,
		}
		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decisionEvent, DummyCardTransferEventConsumerChan), nil

	case CmdDrawCardFromPile:
		decisionEvent := uknow.PlayerDecision{
			Kind: uknow.PlayerDecisionPullFromDeck,
		}

		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decisionEvent, DummyCardTransferEventConsumerChan), nil

	default:
		c.Logger.Printf("Unknown repl command kind: %s", replCommand.Kind.String())
		return uknow.PlayerDecision{}, nil
	}
}

func (c *PlayerClient) sendCommandToUI(uiCommand UICommand, timeout time.Duration) error {
	select {
	case c.GeneralUICommandPushChan <- uiCommand:
		return nil
	case <-time.After(timeout):
		return ErrorUIFailedToConsumeCommand
	}
}

// Connects to each player as given in playerNames and playerListenAddrs and sends a AckNewPlayerAddedMessage response to server for each connected player
func (c *PlayerClient) connectToEachPlayer(ctx context.Context, playerNames []string, playerListenAddrs []utils.TCPAddress) {
	adminURL := fmt.Sprintf("%s/ack_player_added", c.adminAddr.String())

	ctxWithTimeout, _ := context.WithTimeout(ctx, 10*time.Second)
	g, _ := errgroup.WithContext(ctxWithTimeout)

	for i, playerName := range playerNames {
		i := i
		playerName := playerName

		g.Go(func() error {
			c.Logger.Printf("connectToEachPlayer: Local player %s connecting to %s", c.table.LocalPlayerName, playerName)

			_, exists := c.neighborListenAddr[playerName]
			if exists || playerName == c.table.LocalPlayerName {
				return nil
			}

			listenAddr := playerListenAddrs[i]
			pingURL := fmt.Sprintf("%s/ping", listenAddr.HTTPAddress())

			requestSender := utils.RequestSender{
				Client:     c.httpClient,
				Method:     "GET",
				URL:        pingURL,
				BodyReader: nil,
			}

			_, err := requestSender.Send(ctxWithTimeout)
			if err != nil {
				c.Logger.Printf("POST /players - Failed to ping player %s at address %s. Error: %s", playerName, listenAddr.HTTPAddress(), err)
				return ErrorFailedToConnectToNewPlayer
			}

			c.neighborListenAddr[playerName] = listenAddr

			c.Logger.Printf("Local player %s Successfully connected to %s, will send ack to admin", c.table.LocalPlayerName, playerName)

			ackMsg := messages.AckNewPlayerAddedMessage{
				AckerPlayer: c.table.LocalPlayerName,
				NewPlayer:   playerName,
			}

			requestSender = utils.RequestSender{
				Client:     c.httpClient,
				Method:     "POST",
				URL:        adminURL,
				BodyReader: messages.MustJSONReader(&ackMsg),
			}

			resp, err := requestSender.Send(ctxWithTimeout)

			if err != nil || resp.StatusCode != http.StatusOK {
				c.Logger.Printf("Failed to send player_added_ack message for player %s to admin: %s, status: %s", playerName, err, resp.Status)
			} else {
				c.Logger.Printf("Ack successful: %s", resp.Status)
			}
			return err
		})
	}

	err := g.Wait()

	if err != nil {
		c.Logger.Printf("One or more ack messages failed. Error: %s", err)
	}
}
