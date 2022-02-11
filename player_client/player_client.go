package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"github.com/rksht/uknow"
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

type askUIForUserTurnArgs struct {
	receive               chan<- uknow.Command
	timeout               time.Duration
	sender                string
	appQuitting           bool // Set by client to let UI know that the app is stopping
	expectingUserDecision bool // Set by client to make UI expect a user decision command
}

type ClientState string

const (
	clientStateFreeToRespond         ClientState = "free_to_respond"
	clientStateAwaitingAdminApproval             = "awaiting_admin_approval"
)

var ErrorClientAwaitingApproval = errors.New("Client awaiting approval of previous command")
var ErrorClientUnexpectedSender = errors.New("Unexpected sender")
var ErrorFailedToConnectToNewPlayer = errors.New("Failed to connect to new player")

type PlayerClient struct {
	// Used to protect the non-gui state
	stateMutex             sync.Mutex
	table                  *uknow.Table
	allowModifyingCommands bool
	clientState            ClientState
	cmdAwaitingApproval    uknow.Command

	// Server used to service requests made by admin and other players
	httpServer *http.Server

	// Client used to make HTTP requests that are initiated by the user independently of the admin. This
	// includes talking asynchronously to the admin and talking to other players.
	httpClient         *http.Client
	neighborListenAddr ClusterMap
	adminAddr          utils.TCPAddress

	// Exposes the player API to the game admin.
	router *mux.Router

	askUIForUserTurnChan       chan<- askUIForUserTurnArgs
	defaultCommandReceiverChan <-chan uknow.Command
	logWindowChan              chan<- string
	Logger                     *log.Logger

	debugFlags DebugFlags
}

type ConfigNewPlayerClient struct {
	AskUIForUserTurnChan       chan<- askUIForUserTurnArgs
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
		clientState:                clientStateFreeToRespond,
		cmdAwaitingApproval:        uknow.NewCommand(uknow.CmdNone),
		httpClient:                 utils.CreateHTTPClient(),
		neighborListenAddr:         make(ClusterMap),
		askUIForUserTurnChan:       config.AskUIForUserTurnChan,
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

var userDecisionCommands = map[uknow.CommandKind]struct{}{
	uknow.CmdDrawCard:         {},
	uknow.CmdDropCard:         {},
	uknow.CmdDrawCardFromPile: {},
}

type handleAdminCommandResult struct {
	inputCommand        uknow.Command
	adminNeedsToApprove bool // TODO: Get rid of this. Server explicitly sends NeedApproval field
}

// **Does not lock the stateMutex**
func (c *PlayerClient) handleAdminCommand(ctx context.Context, senderName string, cmd uknow.Command) (handleAdminCommandResult, error) {
	var res handleAdminCommandResult

	if c.cmdAwaitingApproval.Kind != uknow.CmdNone {
		return res, ErrorClientAwaitingApproval
	}

	switch cmd.Kind {
	case uknow.CmdAddCards:
		hand, ok := c.table.HandOfPlayer[cmd.TargetPlayerName]
		if !ok {
			return res, errors.New("No player exists in local with name " + cmd.TargetPlayerName)
		}
		for i := 0; i < cmd.Count; i++ {
			card, err := c.table.DrawDeck.Top()
			if err != nil {
				return res, err
			}
			hand = hand.Push(card)
		}
		c.table.HandOfPlayer[cmd.TargetPlayerName] = hand
		return res, nil

	case uknow.CmdMoveCards:
		// TODO: process
		return res, nil

	case uknow.CmdAskUserToPlay:
		// Do something here to tell user that it's their turn
		// c.notifyRedrawUI(..)
		c.allowModifyingCommands = true

		receiveChan := make(chan uknow.Command)

		askUIargs := askUIForUserTurnArgs{
			receive:               receiveChan,
			sender:                uknow.ReservedNameAdmin,
			timeout:               userDecisionTimeout,
			expectingUserDecision: true,
		}

		// Ask UI to receive a decision command from user, with a timeout
		select {
		case c.askUIForUserTurnChan <- askUIargs:
			select {
			case inputCommand := <-receiveChan:
				res.inputCommand = inputCommand
				res.adminNeedsToApprove = true

			case <-ctx.Done():
				return res, errors.New("User did not respond in time")
			}

		case <-ctx.Done():
			return res, ctx.Err()
		}

	default:
		return res, fmt.Errorf("Unimplemented command: %s", cmd.Kind)
	}

	panic("Unreachable")
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
			askUIargs := askUIForUserTurnArgs{
				receive:     uiStopChan,
				appQuitting: true,
				sender:      uknow.ReservedNameClient,
			}

			c.Logger.Printf("Shutdown UI")
			c.askUIForUserTurnChan <- askUIargs
			<-uiStopChan
			break

		case uknow.CmdConnect:
			c.Logger.Printf("Received a connect command...")

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

			var msg utils.AddNewPlayersMessage

			listenAddr, err := utils.ResolveTCPAddress(c.httpServer.Addr)
			if err != nil {
				c.Logger.Fatal(err)
			}

			c.Logger.Printf("Sending listenAddr %+v to admin", listenAddr)

			msg.Add(c.table.LocalPlayerName, listenAddr.Host, listenAddr.Port, "http")

			c.stateMutex.Lock()
			c.connectToAdmin(ctx, msg, adminAddr)
			c.stateMutex.Unlock()

		default:
			c.Logger.Printf("RunDefaultCommandHandler: Unhandled command %s", cmd.Kind)
		}
	}

	log.Print("Exit RunDefaultCommandHandler...")
}

func (c *PlayerClient) connectToAdmin(ctx context.Context, msg utils.AddNewPlayersMessage, adminAddr utils.TCPAddress) {
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
		var respMsg utils.AddNewPlayersMessage
		err := json.NewDecoder(resp.Body).Decode(&respMsg)
		if err != nil {
			c.Logger.Fatalf("connectToAdmin: Failed to read OK response message. %s", err)
		} else {
			c.Logger.Printf("Done connecting to admin")
			c.adminAddr = adminAddr
			c.Logger.Printf("Will connect to currently existing players: %+v", respMsg)

			go c.connectToEachPlayer(ctx, respMsg.PlayerNames, respMsg.ClientListenAddrs)
		}
	default:
		c.Logger.Fatalf("connectToAdmin: Unexpected response: %s", resp.Status)
	}
}

func (c *PlayerClient) printTableInfo(uiState *UIState) {
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

func (c *PlayerClient) addNewPlayer(playerName string, uiState *UIState) {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	c.table.AddPlayer(playerName)

	go func() {
		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()
		uiState.notifyRedrawUI(uiRedraw, func() {
			uiState.refillHandcountChart(c.table)
		})
	}()
}

func (c *PlayerClient) logToWindow(format string, args ...interface{}) {
	format = "Client: " + format
	message := fmt.Sprintf(format, args...)
	c.logWindowChan <- message
	c.Logger.Print(message)
}

func (c *PlayerClient) initRouterHandlers() {
	// All admin commands are serviced via this route. We stay away from REST since it
	// doesn't add much to understandability
	c.router.Path("/command").Methods("POST").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := utils.DecodeCommandPayload(r.Body)
		if err != nil {
			io.WriteString(w, err.Error())
			w.WriteHeader(http.StatusBadRequest)
			c.Logger.Printf("/command: Bad request from sender '%s', error: %s", payload.SenderName, err.Error())
			return
		}

		ourDeadline := time.Now().Add(userDecisionTimeout)
		requestCtx := r.Context()
		requestDeadline, hasDeadline := requestCtx.Deadline()

		var cancelHandle func()
		if !hasDeadline || requestDeadline.After(ourDeadline) {
			requestCtx, cancelHandle = context.WithDeadline(requestCtx, ourDeadline)
			defer cancelHandle()
		}

		if payload.SenderName == uknow.ReservedNameAdmin {
			c.stateMutex.Lock()
			defer c.stateMutex.Unlock()

			if c.clientState == clientStateAwaitingAdminApproval {
				if payload.Command.Kind == uknow.CmdApprove && uknow.CmdApprovesCommand(payload.Command, c.cmdAwaitingApproval) {
					c.clientState = clientStateFreeToRespond
					c.cmdAwaitingApproval = uknow.NewCommand(uknow.CmdNone)
					w.WriteHeader(http.StatusOK)
					return
				}
			}

			result, err := c.handleAdminCommand(requestCtx, payload.SenderName, payload.Command)
			if err != nil {
				c.Logger.Printf("%s", err.Error())

				errorPayload := utils.UnwrappedErrorPayload{}
				errorPayload.Add(err)
				json.NewEncoder(w).Encode(&errorPayload)
				w.WriteHeader(http.StatusNotFound)
				return
			}

			if payload.NeedApproval {
				// Call the admin and expect an ok status
				c.clientState = clientStateAwaitingAdminApproval
				c.cmdAwaitingApproval = result.inputCommand
			}
		}

		w.WriteHeader(http.StatusForbidden)
		errorPayload := utils.UnwrappedErrorPayload{}
		errorPayload.Add(ErrorClientUnexpectedSender)
		json.NewEncoder(w).Encode(&errorPayload)

		// Handle non-admin player command
		c.Logger.Printf("Not responding to senderName `%s`", payload.SenderName)
	})

	c.router.Path("/players").Methods("POST").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg utils.AddNewPlayersMessage

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

	})

	c.router.Path("/players").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.Logger.Printf("Received GET /players")
		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()

		msg := utils.GetPlayersMessage{
			ListenAddrOfPlayer: c.neighborListenAddr,
		}

		if err := json.NewEncoder(w).Encode(&msg); err != nil {
			c.Logger.Printf("GET /players error: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		c.Logger.Printf("Received GET /players. Sending: %+v", msg)
	})

	c.router.Path("/ping").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "pong")
	})

	utils.RoutesSummary(c.router, c.Logger)
}

// Connects to each player as given in playerNames and playerListenAddrs
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

			ackMsg := utils.AckNewPlayerAddedMessage{
				AckerPlayer: c.table.LocalPlayerName,
				NewPlayer:   playerName,
			}

			requestSender = utils.RequestSender{
				Client:     c.httpClient,
				Method:     "POST",
				URL:        adminURL,
				BodyReader: utils.MustJSONReader(&ackMsg),
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
