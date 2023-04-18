package client

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/nrawrx3/uknow"
	messages "github.com/nrawrx3/uknow/internal/messages"
	utils "github.com/nrawrx3/uknow/internal/utils"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type PlayerClientState string

const (
	WaitingToConnectToAdmin       PlayerClientState = "waiting_to_connect_to_admin"
	WaitingForAdminToServeCards   PlayerClientState = "waiting_for_admin_to_serve_cards"
	WaitingForAdminToChoosePlayer PlayerClientState = "waiting_for_admin_to_choose_player"
	AskingUserForDecision         PlayerClientState = "asking_user_for_decision"
	WaitingForDecisionSync        PlayerClientState = "waiting_for_decision_sync"
)

var ErrorFailedToConnectToNewPlayer = errors.New("failed to connect to new player")
var ErrorUIFailedToConsumeCommand = errors.New("ui failed to consume command")
var ErrorFailedToEvaluateReplCommand = errors.New("failed to evaluate repl command")
var ErrorUnimplementedReplCommand = errors.New("unimplemented repl command")

type ClientChannels struct {
	GeneralUICommandPushChan       chan<- UICommand
	AskUserForDecisionPushChan     chan<- *UICommandAskUserForDecision
	NonDecisionReplCommandPullChan <-chan *ReplCommand
	LogWindowPushChan              chan<- string
	GameEventPushChan              chan<- uknow.GameEvent
}
type PlayerClient struct {
	// Used to protect the non-gui state
	stateMutex  sync.Mutex
	clientState PlayerClientState

	table *uknow.Table

	// Server used to service requests made by admin and other players
	// httpServer *http.Server

	aesCipher *uknow.AESCipher

	// NOTE(@rk): We are NOT storing connections to any of the other players
	// in a PlayerClient. We let http.Transport do that for us using its own
	// internal connection pool. We simply keep a map of player name to
	// player address. When the admin sends an AddNewPlayer message via POST
	// /players to a PlayerClient, the client makes a GET /ping on the new
	// player's address to confirm that it can connect. This also creates a
	// connection to this client in http.Transport used by the PlayerClient.
	httpClient         *http.Client
	neighborListenAddr map[string]utils.HostPortProtocol
	adminAddr          utils.HostPortProtocol
	advertiseIP        string

	// Exposes the player API to the game admin.
	router *mux.Router

	ClientChannels

	Logger *log.Logger
}

type ConfigNewPlayerClient struct {
	ClientChannels
	TestErrorChan chan<- error
	Table         *uknow.Table
	// HttpListenAddr             string
	ListenAddr       utils.HostPortProtocol
	DefaultAdminAddr utils.HostPortProtocol
	AdvertiseIP      string
	AESCipher        *uknow.AESCipher
}

func NewPlayerClient(config *ConfigNewPlayerClient) *PlayerClient {
	c := &PlayerClient{
		table:              config.Table,
		clientState:        WaitingToConnectToAdmin,
		httpClient:         utils.CreateHTTPClient(10 * time.Minute),
		neighborListenAddr: make(map[string]utils.HostPortProtocol),
		ClientChannels:     config.ClientChannels,
		Logger:             uknow.CreateFileLogger(false, config.Table.LocalPlayerName),
		adminAddr:          config.DefaultAdminAddr,
		aesCipher:          config.AESCipher,
		advertiseIP:        config.AdvertiseIP,
	}

	c.router = mux.NewRouter()

	c.initRouterHandlers()

	// c.httpServer = &http.Server{
	// 	Addr:    config.ListenAddr.BindString(),
	// 	Handler: c.router,
	// }

	// c.Logger.Printf("Bind address = %s", c.httpServer.Addr)

	// FILTHY(@rk):TODO(@rk): Delete this, see type definition
	// go (&dummyCardTransferEventConsumer{
	// 	decisionEventPullChan: DummyCardTransferEventConsumerChan,
	// }).RunConsumer(c.Logger, c.table.LocalPlayerName)

	return c
}

// func (c *PlayerClient) RunServer() {
// 	c.Logger.Printf("Servicing admin commands at %s", c.httpServer.Addr)
// 	err := c.httpServer.ListenAndServe()
// 	if err != nil {
// 		log.Panicf("PlayerClient.RunServer() failed: %s", err.Error())
// 	}
// }

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
			var adminAddr utils.HostPortProtocol
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

			// listenAddr, err := utils.ResolveTCPAddress(c.httpServer.Addr)
			// if err != nil {
			// 	c.Logger.Fatal(err)
			// }

			msg.Add(c.table.LocalPlayerName, c.advertiseIP, 0, "http")

			// Lock and check if we have the correct state. Connect to admin if yes.
			c.stateMutex.Lock()
			if c.clientState != WaitingToConnectToAdmin {
				c.LogWindowPushChan <- fmt.Sprintf("Invalid command for current state: %s", c.clientState)
				c.stateMutex.Unlock()
				continue
			}
			// c.Logger.Printf("Will be sending listenAddr %+v to admin", listenAddr)
			// c.connectToAdmin(ctx, msg, adminAddr)
			go c.connectToAdminAndStartSSEController(ctx, msg, adminAddr)
			c.stateMutex.Unlock()

		case CmdDeclareReady:
			c.Logger.Printf("Received a declare ready command from UI...")

			url := fmt.Sprintf("%s/set_ready", c.adminAddr.HTTPAddressString())

			setReadyMessage := messages.SetReadyMessage{
				ShufflerName:          c.table.LocalPlayerName,
				ShufflerIsFirstPlayer: false,
			}

			var b bytes.Buffer
			messages.EncodeJSONAndEncrypt(&setReadyMessage, &b, c.aesCipher)

			requestSender := utils.RequestSender{
				Client:     c.httpClient,
				Method:     "POST",
				URL:        url,
				BodyReader: &b,
			}

			resp, err := requestSender.Send(context.TODO())

			if err != nil {
				c.Logger.Print(err)
				break
			}

			if resp.StatusCode != http.StatusOK {
				c.LogWindowPushChan <- "Failed to send declare ready message"
				c.Logger.Printf("Failed to send declare ready message, resp status code: %s", resp.Status)
			}

		case CmdShowHand:
			c.Logger.Printf("Received showhand command from UI...")
			// Just printing to event log window
			c.logToWindow(c.table.HandOfPlayer[c.table.LocalPlayerName].String())

		case CmdTableSummary:
			c.logToWindow("--- table_info:")
			c.logToWindow(fmt.Sprintf(`client_state: %s`, c.clientState))
			c.logToWindow(c.table.Summary())
			c.logToWindow("---")

		case CmdDumpDrawDeck:
			var sb strings.Builder
			c.table.PrintDrawDeck(&sb, cmd.Count)
			c.logToWindow("--- Draw Deck:")
			c.logToWindow(sb.String())

		default:
			c.Logger.Printf("RunDefaultCommandHandler: Unhandled command %s", cmd.Kind)
		}
	}

	log.Print("Exit RunDefaultCommandHandler...")
}

// DOES NOT LOCK stateMutex. Connects to admin and updates state on success.
func (c *PlayerClient) connectToAdmin(ctx context.Context, msg messages.AddNewPlayersMessage, adminAddr utils.HostPortProtocol) {
	var body bytes.Buffer
	err := messages.EncodeJSONAndEncrypt(&msg, &body, c.aesCipher)
	if err != nil {
		c.Logger.Fatal(err)
	}

	// CONSIDER: use ctx along with httpClient.Do
	url := fmt.Sprintf("%s/player", adminAddr.HTTPAddressString())
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
		err := messages.DecryptAndDecodeJSON(&respMsg, resp.Body, c.aesCipher)

		if err != nil {
			c.Logger.Fatalf("connectToAdmin: Failed to read OK response message. %s", err)
		} else {
			c.Logger.Printf("Done connecting to admin")
			c.adminAddr = adminAddr
			c.Logger.Printf("Will connect to currently existing players: %+v", respMsg)

			c.clientState = WaitingForAdminToServeCards
			go c.noteEachPlayer(ctx, respMsg.PlayerNames, respMsg.ClientListenAddrs)
		}
	default:
		c.Logger.Fatalf("connectToAdmin: Unexpected response: %s", resp.Status)
	}
}

func (c *PlayerClient) logToWindow(format string, args ...interface{}) {
	_, file, line, _ := runtime.Caller(1)
	format = c.table.LocalPlayerName + ":" + path.Base(file) + ":" + strconv.FormatInt(int64(line), 10) + " " + format
	message := fmt.Sprintf(format, args...)
	c.LogWindowPushChan <- message
	c.Logger.Print(message)
}

func (c *PlayerClient) initRouterHandlers() {
	c.router.Path("/ping").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "pong")
	})

	// CONVENTION(@rk): See EventMessage for the convention we're using.
	c.router.Path("/event/served_cards").Methods("POST").HandlerFunc(c.handleServedCardsEvent)
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

	err := messages.DecryptAndDecodeJSON(&msg, r.Body, c.aesCipher)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// Set up the connection to the new player without blocking the http response
	// to this call, otherwise we're going to have a deadlock with the other
	// client.
	//
	go func() {
		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()
		c.noteEachPlayer(context.Background(), msg.PlayerNames, msg.ClientListenAddrs)
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

	if err := messages.EncodeJSONAndEncrypt(&msg, w, c.aesCipher); err != nil {
		c.Logger.Printf("GET /players error: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	c.Logger.Printf("Received GET /players. Sending: %+v", msg)
}

func (c *PlayerClient) handleServedCardsEvent(w http.ResponseWriter, r *http.Request) {
	c.Logger.Printf("Received POST /event/served_cards")

	var servedCardsEvent messages.ServedCardsEvent

	err := messages.DecryptAndDecodeJSON(&servedCardsEvent, r.Body, c.aesCipher)

	if err != nil {
		c.Logger.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		messages.WriteErrorPayload(w, err)
		return
	}

	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	if c.clientState != WaitingForAdminToServeCards {
		c.Logger.Printf("Unexpected admin event: %s", servedCardsEvent.EventType())
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	servedCardsEvent.Table.LocalPlayerName = c.table.LocalPlayerName
	c.Logger.Printf("Client: served cards has handOfPlayer: %+v", servedCardsEvent.Table.HandOfPlayer)

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
	c.Logger.Printf("Received POST /event/chosen_player")

	var chosenPlayerEvent messages.ChosenPlayerEvent
	err := messages.DecryptAndDecodeJSON(&chosenPlayerEvent, r.Body, c.aesCipher)

	if err != nil {
		c.Logger.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		messages.WriteErrorPayload(w, err)
		return
	}

	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	if c.clientState != WaitingForAdminToChoosePlayer {
		c.Logger.Printf("Unexpected admin event: %s", chosenPlayerEvent.EventType())
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if c.table.LocalPlayerName == chosenPlayerEvent.PlayerName {
		c.logToWindow("=====Turn end=====")
	} else {
		c.logToWindow("It's player %s's turn", chosenPlayerEvent.PlayerName)
		c.clientState = WaitingForDecisionSync
		return
	}

	c.clientState = AskingUserForDecision
	go c.askAndRunUserDecisions(chosenPlayerEvent.DecisionEventCounter)
}

func (c *PlayerClient) askAndRunUserDecisions(decisionEventCounter int) {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	c.logToWindow("Asking for user decision")
	// c.logToWindow(c.table.Summary())

	receiveReplCommandsChan := make(chan *ReplCommand)
	askUserForDecisionResultChan := make(chan AskUserForDecisionResult)

	askCommand := &UICommandAskUserForDecision{
		receive:            receiveReplCommandsChan,
		decisionResultChan: askUserForDecisionResultChan,
		timeout:            10 * time.Second, // TODO(@rk): Unused and arbitrary. Think later.
		sender:             "PlayerClient",   // TODO(@rk): Unused and arbitrary. Just delete.
	}

	if c.table.TableState == uknow.AwaitingWildDraw4ChallengeDecision {
		askCommand.SetChallengeablePlayer(c.table.PlayerOfLastTurn)
	}

	c.AskUserForDecisionPushChan <- askCommand

	// Now consume the PlayerDecisionEvent(s) and send these to admin

	decisions := make([]uknow.PlayerDecision, 0, 4)

	for replCommand := range receiveReplCommandsChan {
		decision, err := c.evalReplCommandOnTable(replCommand)

		if err != nil {
			var errEvalDecision *uknow.EvalDecisionError
			if errors.As(err, &errEvalDecision) {
				msg := fmt.Sprintf("invalid decision, eligible decisions are: %s", uknow.EligibleCommandsAtState(c.table.TableState))
				c.Logger.Printf(msg)
				c.logToWindow(msg)
			} else {
				c.Logger.Printf("evalReplCommandOnTable failed: %s", err.Error())
			}

			askUserForDecisionResultChan <- AskUserForDecisionResult{
				Error:                 err,
				AskForOneMoreDecision: true, // CONSIDER(@rk): Perhaps we only allow a certain number of retries?
			}
			continue
		}

		c.Logger.Printf("Received replCommand: %s, decisionEvent: %s", replCommand.Kind.String(), &decision)

		decisions = append(decisions, decision)

		askUserForDecisionResultChan <- AskUserForDecisionResult{
			AskForOneMoreDecision: c.table.NeedMoreUserDecisionToFinishTurn(),
		}
	}

	c.Logger.Printf("Done receiving player decision events from ClientUI")

	// Send decisions to admin
	requestBody := messages.PlayerDecisionsRequest{
		Decisions:            decisions,
		DecidingPlayer:       c.table.LocalPlayerName,
		DecisionEventCounter: decisionEventCounter,
	}

	var b bytes.Buffer
	messages.EncodeJSONAndEncrypt(&requestBody, &b, c.aesCipher)

	requester := utils.RequestSender{
		URL:        fmt.Sprintf("%s/%s", c.adminAddr.HTTPAddressString(), requestBody.RestPath()),
		Method:     "POST",
		Client:     c.httpClient,
		BodyReader: &b,
	}

	c.Logger.Printf("Sending decisions to admin: %+v", requestBody)

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

// POST /player_decisions_sync
func (c *PlayerClient) handlePlayerDecisionsSyncEvent(w http.ResponseWriter, r *http.Request) {
	c.Logger.Printf("Received player_decisions_sync message")
	var decisionsEvent messages.PlayerDecisionsSyncEvent
	err := messages.DecryptAndDecodeJSON(&decisionsEvent, r.Body, c.aesCipher)

	if err != nil {
		c.Logger.Print(err)
		w.WriteHeader(http.StatusInternalServerError)
		messages.WriteErrorPayload(w, err)
		return
	}

	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	// Admin sends the sync event to all clients, including the client who generated the decisions. Handling this.
	if decisionsEvent.DecidingPlayer == c.table.LocalPlayerName {
		return
	}

	if c.clientState != WaitingForDecisionSync {
		c.Logger.Printf("Unexpected event %s received while in state %s", decisionsEvent.RestPath(), c.clientState)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Eval decisions of other player
	c.Logger.Printf("Evaluating player %s's %d decisions: %+v", decisionsEvent.DecidingPlayer, len(decisionsEvent.Decisions), decisionsEvent)
	c.table.EvalPlayerDecisions(decisionsEvent.DecidingPlayer, decisionsEvent.Decisions, c.GameEventPushChan)

	c.Logger.Printf("Done evaluating player %s's %d decisions", decisionsEvent.DecidingPlayer, len(decisionsEvent.Decisions))

	c.clientState = WaitingForAdminToChoosePlayer
}

// Maps the repl command to a PlayerDecision and evaluates it on the table with
// local player as the deciding player.
func (c *PlayerClient) evalReplCommandOnTable(replCommand *ReplCommand) (uknow.PlayerDecision, error) {
	switch replCommand.Kind {
	case CmdDropCard:
		decision := uknow.PlayerDecision{
			Kind:       uknow.PlayerDecisionPlayHandCard,
			ResultCard: replCommand.Cards[0],
		}

		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decision, c.GameEventPushChan)

	case CmdDrawCard:
		decision := uknow.PlayerDecision{
			Kind: uknow.PlayerDecisionPullFromDeck,
		}
		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decision, c.GameEventPushChan)

	case CmdDrawCardFromPile:
		decision := uknow.PlayerDecision{
			Kind: uknow.PlayerDecisionPullFromDeck,
		}

		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decision, c.GameEventPushChan)

	case CmdSetWildCardColor:
		chosenColor, ok := replCommand.ExtraData.(uknow.Color)
		if !ok {
			return uknow.PlayerDecision{}, uknow.ErrShouldNotHappen
		}

		decision := uknow.PlayerDecision{
			Kind:                uknow.PlayerDecisionWildCardChooseColor,
			WildCardChosenColor: chosenColor,
		}

		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decision, c.GameEventPushChan)

	case CmdChallenge:
		decision := uknow.PlayerDecision{
			Kind:                uknow.PlayerDecisionDoChallenge,
			WildCardChosenColor: c.table.RequiredColorOfCurrentTurn,
		}
		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decision, c.GameEventPushChan)

	case CmdNoChallenge:
		decision := uknow.PlayerDecision{
			Kind:                uknow.PlayerDecisionDontChallenge,
			WildCardChosenColor: c.table.RequiredColorOfCurrentTurn,
		}
		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decision, c.GameEventPushChan)

	case CmdPass:
		decision := uknow.PlayerDecision{
			Kind: uknow.PlayerDecisionPass,
		}

		return c.table.EvalPlayerDecision(c.table.LocalPlayerName, decision, c.GameEventPushChan)

	default:
		c.Logger.Printf("Unknown repl command kind: %s", replCommand.Kind.String())
		return uknow.PlayerDecision{}, ErrorUnimplementedReplCommand
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
func (c *PlayerClient) noteEachPlayer(ctx context.Context, playerNames []string, playerListenAddrs []utils.HostPortProtocol) {
	c.logToWindow("noting each player and sending ack: %+v", playerNames)

	adminURL := fmt.Sprintf("%s/ack_player_added", c.adminAddr.HTTPAddressString())

	ctxWithTimeout, _ := context.WithTimeout(ctx, 10*time.Second)
	g, _ := errgroup.WithContext(ctxWithTimeout)

	for _, playerName := range playerNames {
		playerName := playerName

		g.Go(func() error {
			c.Logger.Printf("Local player %s noted %s, will send ack to admin", c.table.LocalPlayerName, playerName)

			ackMsg := messages.AckNewPlayerAddedMessage{
				AckerPlayer: c.table.LocalPlayerName,
				NewPlayer:   playerName,
			}

			var b bytes.Buffer
			messages.EncodeJSONAndEncrypt(&ackMsg, &b, c.aesCipher)

			requestSender := utils.RequestSender{
				Client:     c.httpClient,
				Method:     "POST",
				URL:        adminURL,
				BodyReader: &b,
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

func (c *PlayerClient) connectToAdminAndStartSSEController(ctx context.Context, msg messages.AddNewPlayersMessage, adminAddr utils.HostPortProtocol) {
	var requestBody bytes.Buffer
	if err := messages.EncodeJSONAndEncrypt(&msg, &requestBody, c.aesCipher); err != nil {
		c.Logger.Fatal(err)
	}

	url := fmt.Sprintf("%s/player", adminAddr.HTTPAddressString())

	c.logToWindow("Calling %s", url)

	req, err := http.NewRequest("POST", url, &requestBody)
	if err != nil {
		c.logToWindow("failed to create request toi %s: %v", url, err)
		return
	}

	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logToWindow("failed to connect to admin: %v", err)
		return
	}

	c.logToWindow("POST %s %+v response code: %s", url, msg, resp.Status)

	switch resp.StatusCode {
	case http.StatusSeeOther:
		c.logToWindow("connectToAdmin: Local player is already present in admin's table")
	case http.StatusOK:
		c.sseController(resp)
	}
}
