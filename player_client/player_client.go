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

const userDecisionTimeout = time.Duration(10) * time.Second

type PlayerClientState string

const (
	WaitingToConnectToAdmin       PlayerClientState = "waiting_to_connect_to_admin"
	WaitingForAdminToServeCards   PlayerClientState = "waiting_for_admin_to_serve_cards"
	WaitingForAdminToChoosePlayer PlayerClientState = "waiting_for_admin_to_choose_player"
	AskingUserForDecisionCommand  PlayerClientState = "asking_user_for_decision_command"
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

var ErrorClientAwaitingApproval = errors.New("Client awaiting approval of previous command")
var ErrorClientUnexpectedSender = errors.New("Unexpected sender")
var ErrorFailedToConnectToNewPlayer = errors.New("Failed to connect to new player")
var ErrorUIFailedToConsumeCommand = errors.New("UI Failed to consume command")
var ErrorUnknownEvent = errors.New("Unknown event")

type PlayerClient struct {
	// Used to protect the non-gui state
	stateMutex  sync.Mutex
	clientState PlayerClientState

	table               *uknow.Table
	cmdAwaitingApproval uknow.Command

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

	generalUICommandChan       chan<- UICommand
	askForUserInputChan        chan<- *UICommandAskForUserInput
	defaultCommandReceiverChan <-chan uknow.Command
	logWindowChan              chan<- string
	Logger                     *log.Logger

	debugFlags DebugFlags
}

type ConfigNewPlayerClient struct {
	GeneralUICommandChan       chan<- UICommand
	AskUIForUserTurnChan       chan<- *UICommandAskForUserInput
	DefaultCommandReceiverChan <-chan uknow.Command
	LogWindowChan              chan<- string
	TestErrorChan              chan<- error
	Table                      *uknow.Table
	// HttpListenAddr             string
	ListenAddr       utils.TCPAddress
	DefaultAdminAddr utils.TCPAddress
}

func NewPlayerClient(config *ConfigNewPlayerClient, debugFlags DebugFlags) *PlayerClient {
	c := &PlayerClient{
		table:                      config.Table,
		clientState:                WaitingToConnectToAdmin,
		cmdAwaitingApproval:        uknow.NewCommand(uknow.CmdNone),
		httpClient:                 utils.CreateHTTPClient(),
		neighborListenAddr:         make(ClusterMap),
		generalUICommandChan:       config.GeneralUICommandChan,
		askForUserInputChan:        config.AskUIForUserTurnChan,
		defaultCommandReceiverChan: config.DefaultCommandReceiverChan,
		logWindowChan:              config.LogWindowChan,
		Logger:                     utils.CreateFileLogger(false, config.Table.LocalPlayerName),
		debugFlags:                 debugFlags,
		adminAddr:                  config.DefaultAdminAddr,
	}

	c.router = mux.NewRouter()

	c.initRouterHandlers()

	c.httpServer = &http.Server{
		Addr:    config.ListenAddr.BindString(),
		Handler: c.router,
	}

	c.Logger.Printf("Addr bind string = %s", config.ListenAddr.BindString())
	c.Logger.Printf("Bind address = %s", c.httpServer.Addr)

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
func (c *PlayerClient) RunDefaultCommandHandler() {
	c.Logger.Printf("%s - running default command handler", c.table.LocalPlayerName)

	ctx := context.Background()

	for cmd := range c.defaultCommandReceiverChan {
		// Logging for now
		c.Logger.Printf("default cmd `%+v`", cmd)

		switch cmd.Kind {
		case uknow.CmdQuit:
			c.Logger.Printf("Shutdown server")
			c.httpServer.Shutdown(context.Background())

			uiStopChan := make(chan uknow.Command)
			askUIargs := &UICommandAskForUserInput{
				receive:     uiStopChan,
				appQuitting: true,
				sender:      uknow.ReservedNameClient,
			}

			c.Logger.Printf("Shutdown UI")
			c.askForUserInputChan <- askUIargs
			<-uiStopChan

		case uknow.CmdConnect:
			c.Logger.Printf("Received a connect command from UI...")

			// Building the message struct first since it doesn't depend on mutable state
			var adminAddr utils.TCPAddress
			var err error

			if adminAddrString, ok := cmd.ExtraData.(string); ok {
				adminAddr, err = utils.ResolveTCPAddress(adminAddrString)
				if err != nil {
					c.Logger.Print(err)
					c.logWindowChan <- fmt.Sprint(err)
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
				c.logWindowChan <- fmt.Sprintf("Invalid command for current state: %s", c.clientState)
				c.stateMutex.Unlock()
				continue
			}
			c.Logger.Printf("Will sending listenAddr %+v to admin", listenAddr)
			c.connectToAdmin(ctx, msg, adminAddr)
			c.stateMutex.Unlock()

		case uknow.CmdDeclareReady:
			c.Logger.Printf("Received a declare ready command from UI...")

			url := fmt.Sprintf("%s/set_ready", c.adminAddr.String())

			setReadyMessage := messages.SetReadyMessage{
				ShufflerName:          c.table.LocalPlayerName,
				ShufflerIsFirstPlayer: true,
			}

			resp, err := c.httpClient.Post(url, "application/json", messages.MustJSONReader(&setReadyMessage))

			if err != nil {
				c.Logger.Print(err)
			}

			if resp.StatusCode != http.StatusOK {
				c.logWindowChan <- "Failed to send declare ready message"
				c.Logger.Printf("Failed to send declare ready message, resp status code: %s", resp.Status)
			}

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

	// TODO: use ctx along with httpClient.Do
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

func (c *PlayerClient) printTableInfo(uiState *ClientUI) {
	handCounts := make(map[string]int)
	for playerName, hand := range c.table.HandOfPlayer {
		handCounts[playerName] = len(hand)
	}

	msg := fmt.Sprintf(`
Players:	%+v,
Hand counts:	%+v,
DrawDeck count: %+v,
DiscardPile count: %+v`,
		c.table.PlayerNames, handCounts, len(c.table.DrawDeck), len(c.table.Pile))
	uiState.appendEventLog(msg)
	c.Logger.Printf(msg)
	c.logWindowChan <- msg
}

func (c *PlayerClient) logToWindow(format string, args ...interface{}) {
	format = "Client: " + format
	message := fmt.Sprintf(format, args...)
	c.logWindowChan <- message
	c.Logger.Print(message)
}

func (c *PlayerClient) initRouterHandlers() {
	c.router.Path("/ping").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "pong")
	})

	c.router.Path("/event/served_cards").Methods("POST").HandlerFunc(c.handleServedCardEvent)

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

	uiCommand := &UICommandSetServedCards{
		table: &servedCardsEvent.Table,
	}

	err = c.sendCommandToUI(uiCommand, 2*time.Second)
	if err != nil {
		c.Logger.Print(err)
	}

	c.clientState = WaitingForAdminToChoosePlayer
}

func (c *PlayerClient) sendCommandToUI(uiCommand UICommand, timeout time.Duration) error {
	select {
	case c.generalUICommandChan <- uiCommand:
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
