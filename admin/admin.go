package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chzyer/readline"
	"github.com/gorilla/mux"
	"github.com/nrawrx3/uknow"
	"github.com/nrawrx3/uknow/hand_reader"
	"github.com/nrawrx3/uknow/internal/messages"
	"github.com/nrawrx3/uknow/internal/utils"
	"golang.org/x/sync/errgroup"
)

var (
	// adminUserConfig AdminUserConfig

	errorWaitingForAcks    = errors.New("waiting for acks")
	errorInvalidAdminState = errors.New("invalid admin state")
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

func makeAckIdConnectedPlayer(ackerPlayer, connectedPlayer string) string {
	return fmt.Sprintf("%s_connected_to_%s", ackerPlayer, connectedPlayer)
}

func makeAckIdOfDecisionSyncPlayer(ackerPlayer string, decisionCounter int) string {
	return fmt.Sprintf("%s_synced_%d", ackerPlayer, decisionCounter)
}

func makeAckIdWaitingForPlayerDecision(ackerPlayer string, decisionCounter int) string {
	return fmt.Sprintf("waiting_for_decision.%s.%d", ackerPlayer, decisionCounter)
}

const pauseBeforeChoosingPlayer = 2 * time.Second

type Admin struct {
	table      *uknow.Table
	stateMutex sync.Mutex
	state      AdminState

	userConfig *AdminUserConfig
	aesCipher  *uknow.AESCipher

	// Address of player registered on connect command
	listenAddrOfPlayer map[string]utils.HostPortProtocol // TODO: Deprecate after SSE based communication is implemented

	sseWriterForPlayer map[string]sseWriter

	shuffler                string
	readyPlayerName         string
	httpServer              *http.Server
	logger                  *log.Logger
	decisionEventsCompleted int

	expectedAcksList *expectedAcksList
	rl               *readline.Instance

	sseControllerEventChan chan sseEvent
	sseControllerStopChan  chan struct{}
}

type sseWriter struct {
	responseWriter http.ResponseWriter
}

func (w *sseWriter) writeEventMessage(ctx context.Context, event messages.ServerEvent) error {
	eventMessage := messages.ServerEventMessage{
		Event: event,
		Type:  event.EventType(),
	}
	if err := utils.WriteJsonWithNewline(w.responseWriter, eventMessage); err != nil {
		return err
	}
	if flusher, ok := w.responseWriter.(http.Flusher); ok {
		flusher.Flush()
	} else {
		log.Printf("ERROR: responseWriter doesn't implement http.Flusher - needed for SSE based messages!")
	}
	return nil
}

// sseEvent is an interface that is implemented by all the events that
// are sent to the SSE controller. The sseControllerEventXXX structs have name
// corresponding to the event message structs in messages package.
type sseEvent interface {
	IsSseEvent()
}

type sseCommandSyncPlayerJoinedEventToAll struct {
	NewPlayerName        string
	ResponseWriter       http.ResponseWriter
	NotifyControllerExit chan<- struct{}
}

func (sseCommandSyncPlayerJoinedEventToAll) IsSseEvent() {}

type sseCommandSendServedCardsEventToAll struct {
	Table uknow.Table
}

func (sseCommandSendServedCardsEventToAll) IsSseEvent() {}

type sseCommandSendChosenPlayerEventToAll struct {
}

func (sseCommandSendChosenPlayerEventToAll) IsSseEvent() {}

type sseCommandSyncPlayerDecisionEvent struct {
	messages.PlayerDecisionsRequest
}

func (sseCommandSyncPlayerDecisionEvent) IsSseEvent() {}

type ConfigNewAdmin struct {
	ListenAddr      utils.HostPortProtocol
	Table           *uknow.Table
	ReadyPlayerName string
	aesCipher       *uknow.AESCipher
}

const logFilePrefix = "admin"

func NewAdmin(config *ConfigNewAdmin, userConfig *AdminUserConfig) *Admin {
	logger := uknow.CreateFileLogger(false, logFilePrefix)
	admin := &Admin{
		table:                  config.Table,
		userConfig:             userConfig,
		listenAddrOfPlayer:     make(map[string]utils.HostPortProtocol),
		sseWriterForPlayer:     make(map[string]sseWriter),
		shuffler:               "",
		aesCipher:              config.aesCipher,
		state:                  AddingPlayers,
		expectedAcksList:       newExpectedAcksState(logger),
		logger:                 logger,
		readyPlayerName:        config.ReadyPlayerName,
		sseControllerEventChan: make(chan sseEvent),
		sseControllerStopChan:  make(chan struct{}),
	}

	r := admin.setRouterHandlers()

	admin.httpServer = &http.Server{
		Handler: r,
		Addr:    config.ListenAddr.BindString(),

		// TODO: Experiment with low-ish write timeouts - we're writing
		// text/event-stream so the admin should have fairly long write timeouts
		// (the duration of a game basically). Read timeouts however can be short as
		// admin itself doesn't read any stream message.
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  10 * time.Minute,
	}

	go admin.runSSEController()

	return admin
}

func (admin *Admin) setRouterHandlers() *mux.Router {
	r := mux.NewRouter()
	r.Path("/player").Methods("POST").HandlerFunc(admin.handleAddNewPlayerAndCreateSSE)
	r.Path("/ack_player_added").Methods("POST").HandlerFunc(admin.handleAckNewPlayerAdded)
	r.Path("/set_ready").Methods("POST").HandlerFunc(admin.handleSetReady)
	r.Path("/player_decisions").Methods("POST").HandlerFunc(admin.handlePlayerDecisionsEvent)
	r.Path("/ack-decision-sync").Methods("POST").HandlerFunc(admin.handleAckPlayerDecisionSynced)
	r.Path("/test_command").Methods("POST")
	utils.RoutesSummary(r, admin.logger)
	return r
}

func (admin *Admin) Restart() {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	admin.table = createStartingTable(admin.userConfig)

	admin.logger = uknow.CreateFileLogger(false, logFilePrefix)

	admin.listenAddrOfPlayer = make(map[string]utils.HostPortProtocol)
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

// Increase this timeout before debugging.
// TODO: Have a config for this timeout
const allPlayersSyncCommandTimeout = time.Duration(10) * time.Second

// const allPlayersSyncCommandTimeout = time.Duration(10000) * time.Second

// Req:		POST /player AddNewPlayerMessage
// Resp:	AddNewPlayerMessage
func (admin *Admin) handleAddNewPlayerAndCreateSSE(w http.ResponseWriter, r *http.Request) {
	admin.logger.Printf("addNewPlayer receeived from %s", r.RemoteAddr)
	admin.stateMutex.Lock()

	if admin.state != AddingPlayers {
		fmt.Fprintf(w, "Not accepting new players, currently in state: %s", admin.state)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var requestMessage messages.AddNewPlayersMessage
	if err := messages.DecryptAndDecodeJSON(&requestMessage, r.Body, admin.aesCipher); err != nil {
		admin.logger.Printf("failed to decode add new player request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(requestMessage.PlayerNames) == 0 {
		http.Error(w, fmt.Sprintf("Expected exactly 1 player name, got %d", len(requestMessage.PlayerNames)), http.StatusBadRequest)
	}

	joinerPlayerName := requestMessage.PlayerNames[0]
	utils.SetSSEResponseHeaders(w)

	// Add the player to the local table. **But don't if it's already added
	// by hand-reader - in which case check that we have this player in the
	// table module.**
	if admin.table.IsShuffled {
		_, ok := admin.table.HandOfPlayer[joinerPlayerName]
		if !ok {
			admin.logger.Printf("player %s has not been loaded by hand-reader. see the JSON config.", joinerPlayerName)
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
	} else {
		err := admin.table.AddPlayer(joinerPlayerName)
		if errors.Is(err, uknow.ErrPlayerAlreadyExists) {
			w.WriteHeader(http.StatusOK)
			admin.logger.Printf("player %s already exists", joinerPlayerName)
			return
		}

		if err != nil {
			http.Error(w, fmt.Sprintf("cannot add new player: %s", err), http.StatusUnprocessableEntity)
			admin.logger.Printf("Cannot add new player: %s", err)
			return
		}
	}

	// Rest of the work is going to be done in the controller
	notifyControllerExit := make(chan struct{})

	go func() {
		admin.sseControllerEventChan <- sseCommandSyncPlayerJoinedEventToAll{
			NewPlayerName:        joinerPlayerName,
			ResponseWriter:       w,
			NotifyControllerExit: notifyControllerExit,
		}
	}()

	admin.stateMutex.Unlock()
	// Prevent returning from this handler until controller notifies.
	//
	// TODO: No notification yet. Just pauses indefinitely.
	<-notifyControllerExit
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

	// Find and remove from this ack from the expectingAcks list.
	ack := expectedAck{
		ackId:           makeAckIdConnectedPlayer(reqBody.AckerPlayer, reqBody.NewPlayer),
		ackerPlayerName: reqBody.AckerPlayer,
	}

	admin.logger.Printf("ack: %+v", ack)

	admin.expectedAcksList.chNewAckReceived <- ack
	w.WriteHeader(http.StatusOK)
}

func (admin *Admin) handleAckPlayerDecisionSynced(w http.ResponseWriter, r *http.Request) {
	var reqBody messages.AckSyncedPlayerDecisionsMesasge
	if err := messages.DecryptAndDecodeJSON(&reqBody, r.Body, admin.aesCipher); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		messages.WriteErrorPayload(w, err)
		return
	}

	ack := expectedAck{
		ackId:           makeAckIdOfDecisionSyncPlayer(reqBody.AckerPlayer, reqBody.DecisionCounter),
		ackerPlayerName: reqBody.AckerPlayer,
	}

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

	admin.logger.Printf("acquired expectedAcksList.mu.Lock()")

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
	}

	// go admin.sendServeCardsEventToAllPlayers()
	go func() {
		admin.sseControllerEventChan <- sseCommandSendServedCardsEventToAll{
			Table: *admin.table,
		}
	}()
}

// Req: POST /player_decisions_event
func (admin *Admin) handlePlayerDecisionsEvent(w http.ResponseWriter, r *http.Request) {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	switch admin.state {
	case WaitingForPlayerDecision:
		var event messages.PlayerDecisionsRequest
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

		go func() {
			admin.sseControllerEventChan <- sseCommandSyncPlayerDecisionEvent{
				PlayerDecisionsRequest: event,
			}
		}()

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

func (admin *Admin) runNewTurn() {
	if admin.userConfig.DebugSignalNewTurnViaPrompt {
		log.Printf("Waiting for `newturn` command before starting turn")
		return
	}

	<-time.After(time.Duration(admin.userConfig.PauseMsecsBeforeNewTurn) * time.Millisecond)

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

		go func() {
			admin.sseControllerEventChan <- sseCommandSendChosenPlayerEventToAll{}
		}()
	}
}

func (admin *Admin) runSSEController() {
	admin.logger.Printf("runSSEController: Starting...")

	for {
		select {
		case <-admin.sseControllerStopChan:
			admin.logger.Printf("runSSEController: Stopping...")
			return
		case ctlEvent := <-admin.sseControllerEventChan:
			admin.logger.Printf("runSSEController: Received event: %+v, type: %T", ctlEvent, ctlEvent)
			admin.dispatchEventWithSSE(ctlEvent)
		}
	}
}

// dispatchEventWithSSE will block until whatever it's supposed to do finishes.
// When sender code does not want to block on a send to
// admin.sseControllerEventChan, it should do so via a new goroutine. We are
// actually doing just that. dispatchEventWithSSE always requires holding the
// stateMutex so a synchronous call from a handler will deadlock.
func (admin *Admin) dispatchEventWithSSE(ctlEvent sseEvent) {
	switch e := ctlEvent.(type) {
	case sseCommandSyncPlayerJoinedEventToAll:
		func() {
			admin.stateMutex.Lock()
			admin.sseWriterForPlayer[e.NewPlayerName] = sseWriter{responseWriter: e.ResponseWriter}
			admin.stateMutex.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), allPlayersSyncCommandTimeout)
			defer cancel()
			g, ctx := errgroup.WithContext(ctx)

			// Send the newly joined player's name to all existing players.
			g.Go(func() error {
				admin.stateMutex.Lock()
				defer admin.stateMutex.Unlock()
				if err := admin.sendMessageToAllPlayersWithSSE(ctx, e.NewPlayerName, messages.PlayerJoinedEvent{PlayerName: e.NewPlayerName}); err != nil {
					log.Printf("ERROR: failed to send joined player event to player: %v", err)
					return err
				}
				return nil
			})

			// Send the list of all existing players to the newly joined player.
			g.Go(func() error {
				admin.stateMutex.Lock()
				defer admin.stateMutex.Unlock()

				existingPlayersMsg := messages.ExistingPlayersListEvent{
					PlayerNames: make([]string, 0, len(admin.sseWriterForPlayer)),
				}
				for existingPlayerName := range admin.sseWriterForPlayer {
					existingPlayerName := existingPlayerName

					if existingPlayerName == e.NewPlayerName {
						continue
					}
					existingPlayersMsg.PlayerNames = append(existingPlayersMsg.PlayerNames, existingPlayerName)

					// Create an expecting-ack for the newly joined player itself.
					admin.expectedAcksList.addPending(
						expectedAck{
							ackId:           makeAckIdConnectedPlayer(e.NewPlayerName, existingPlayerName),
							ackerPlayerName: e.NewPlayerName,
						},
						5*time.Second,
						func() {
							admin.logger.Printf("%s acked %s", e.NewPlayerName, existingPlayerName)
						},
						func() {
							admin.logger.Printf("ack timeout: new player %s did not ack existing player %s in time", e.NewPlayerName, existingPlayerName)
						},
					)

					// Create expecting-ack for each of the existing players also.
					admin.expectedAcksList.addPending(
						expectedAck{
							ackId:           makeAckIdConnectedPlayer(existingPlayerName, e.NewPlayerName),
							ackerPlayerName: existingPlayerName,
						},
						5*time.Second,
						func() {
							admin.logger.Printf("%s acked %s", existingPlayerName, e.NewPlayerName)
						},
						func() {
							admin.logger.Printf("ack timeout: existing player %s did not ack new plater %s in time", existingPlayerName, e.NewPlayerName)
						},
					)
				}

				return admin.sendMessageToSinglePlayerWithSSE(ctx, e.NewPlayerName, existingPlayersMsg)
			})
			if err := g.Wait(); err != nil {
				log.Printf("Failed to sync player join: %v", err)
			}
		}()

	case sseCommandSyncPlayerDecisionEvent:
		func() {
			admin.stateMutex.Lock()
			defer admin.stateMutex.Unlock()

			// Evaluate the decisions on the admin table
			err := admin.table.EvalPlayerDecisionsNoTransferChan(e.DecidingPlayer, e.Decisions)
			if err != nil {
				admin.logger.Printf("ERROR while evaluating decision on admin board: %v, %+v", err, e.PlayerDecisionsRequest)
				return
			}

			// Sync the player decision with all other players
			admin.setState(SyncingPlayerDecision)
			admin.updatePromptWithStateInfo()

			err = admin.sendMessageToAllPlayersWithSSE(context.Background(), e.PlayerDecisionsRequest.DecidingPlayer, messages.PlayerDecisionsSyncEvent{PlayerDecisionsRequest: e.PlayerDecisionsRequest})
			if err != nil {
				admin.logger.Printf("Failed to broadcast player decisions event: %v", err)
				return
			}

			var remainingAcksBeforeDoneSyncing atomic.Int32
			remainingAcksBeforeDoneSyncing.Store(int32(len(admin.sseWriterForPlayer)) - 1)

			// Since we're using SSE, instead of HTTP request-response, we need asynchronous acking of the decisions being synced by the server.
			for playerName := range admin.sseWriterForPlayer {
				if playerName == e.DecidingPlayer {
					continue
				}
				admin.expectedAcksList.addPending(
					expectedAck{
						ackId:           makeAckIdOfDecisionSyncPlayer(playerName, e.DecisionEventCounter),
						ackerPlayerName: playerName,
					},
					5*time.Second,
					func() {
						admin.logger.Printf("%s acked decision %d", playerName, e.DecisionEventCounter)
						if remainingAcksBeforeDoneSyncing.Add(-1) == 0 {
							admin.stateMutex.Lock()
							admin.setState(DoneSyncingPlayerDecision)
							admin.decisionEventsCompleted++
							admin.stateMutex.Unlock()
							go admin.runNewTurn()
						}
					},
					func() {
						admin.logger.Printf("ack timeout: existing player %s did not ack decision %d in time", playerName, e.DecisionEventCounter)
					},
				)
			}

			// admin.setState(DoneSyncingPlayerDecision)
			// go admin.runNewTurn()
		}()

	case sseCommandSendServedCardsEventToAll:
		func() {
			admin.stateMutex.Lock()
			defer admin.stateMutex.Unlock()
			eventMsg := &messages.ServedCardsEvent{
				Table: *admin.table,
			}
			if err := admin.sendMessageToAllPlayersWithSSE(context.Background(), "", eventMsg); err != nil {
				log.Printf("ERROR: failed to send served cards event to player: %v", err)
			}

			admin.logger.Printf("Waiting %.0f seconds before sending chosen player event", pauseBeforeChoosingPlayer.Seconds())
			<-time.After(pauseBeforeChoosingPlayer)

			admin.logger.Printf("Next turn: %s", admin.table.PlayerOfNextTurn)

			go func() {
				admin.sseControllerEventChan <- sseCommandSendChosenPlayerEventToAll{}
			}()
		}()

	case sseCommandSendChosenPlayerEventToAll:
		func() {
			admin.stateMutex.Lock()
			defer admin.stateMutex.Unlock()
			eventMsg := messages.ChosenPlayerEvent{
				PlayerName:           admin.table.PlayerOfNextTurn,
				DecisionEventCounter: admin.decisionEventsCompleted,
			}
			if err := admin.sendMessageToAllPlayersWithSSE(context.Background(), "", &eventMsg); err != nil {
				admin.logger.Printf("sendMessageToAllPlayersWithSSE failed to send chosen player message: %v", err)
				return
			}
			admin.logger.Printf("success: sent chosen player message to all players: %+v", eventMsg)

			admin.setState(WaitingForPlayerDecision)

			admin.expectedAcksList.addPending(
				expectedAck{
					ackId:           makeAckIdWaitingForPlayerDecision(eventMsg.PlayerName, admin.decisionEventsCompleted),
					ackerPlayerName: eventMsg.PlayerName,
				},
				// 20*time.Second,
				1*time.Hour,
				func() {},
				func() {
					admin.logger.Printf("Ack timeout: Failed to receive player decision event from player %s", eventMsg.PlayerName)
				},
			)
		}()
	}
}

func (admin *Admin) sendMessageToAllPlayersWithSSE(ctx context.Context, excludePlayer string, eventMsg messages.ServerEvent) error {
	// TODO: Call in parallel. Use timeout via ctx.Done. Also, to avoid race conditions, clone the map - but it's unlikely.
	admin.logger.Printf("sendMessageToAllPlayersWithSSE: (excluded: %s) %T %+v", excludePlayer, eventMsg, eventMsg)
	for playerName, writer := range admin.sseWriterForPlayer {
		if playerName == excludePlayer {
			continue
		}
		if err := writer.writeEventMessage(ctx, eventMsg); err != nil {
			return err
		}
	}
	return nil
}

func (admin *Admin) sendMessageToSinglePlayerWithSSE(ctx context.Context, playerName string, eventMsg messages.ServerEvent) error {
	admin.logger.Printf("sendMessageToSinglePlayerWithSSE: %s %T %+v", playerName, eventMsg, eventMsg)
	writer, exists := admin.sseWriterForPlayer[playerName]
	if !exists {
		return uknow.ErrUnknownPlayer
	}
	return writer.writeEventMessage(ctx, eventMsg)
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

		if line == "table_summary" {
			admin.stateMutex.Lock()
			log.Print(admin.table.Summary())
			admin.stateMutex.Unlock()
			continue
		}

		if line == "show_hands" {
			var sb strings.Builder
			admin.table.PrintHands(&sb)
			admin.logger.Print(sb.String())
			log.Print(sb.String())
		}

		if line == "dump_drawdeck" {
			var sb strings.Builder
			admin.table.PrintDrawDeck(&sb, 15)
			admin.logger.Print(sb.String())
			log.Print(sb.String())
		}

		if line == "set_ready" || line == "sr" {
			admin.setReady()
		}
	}
}

// If there's a starting hand-config specified for debugging, we create a table accordingly
func createStartingTable(c *AdminUserConfig) *uknow.Table {
	tableLogger := uknow.CreateFileLogger(false, "table_admin")
	table := uknow.NewAdminTable(tableLogger)
	var err error

	if c.DebugStartingHandConfigFile != "" {
		table, err = hand_reader.LoadConfigFromFile(c.DebugStartingHandConfigFile, table, tableLogger)
	} else if c.DebugStartingHandConfig != nil {
		table, err = hand_reader.LoadConfig(c.DebugStartingHandConfig, table, log.Default())
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

func LoadConfig(configFile string) (AdminUserConfig, *uknow.AESCipher) {
	f, err := os.Open(configFile)
	if err != nil {
		log.Fatalf("failed to open config file %s: %v", configFile, err)
	}
	defer f.Close()

	configBytes, err := io.ReadAll(f)
	if err != nil {
		log.Fatalf("failed to read config file %s: %v", configFile, err)
	}

	var adminConfig AdminUserConfig
	err = json.NewDecoder(bytes.NewReader(configBytes)).Decode(&adminConfig)
	if err != nil {
		log.Fatalf("failed to parse admin config: %v", err)
	}

	if adminConfig.Type != "admin" {
		log.Fatalf("expected \"type\" field in config to have value \"admin\"")
	}

	var aesCipher *uknow.AESCipher
	if adminConfig.EncryptMessages {
		aesCipher, err = uknow.NewAESCipher(adminConfig.AESKeyString)
		if err != nil {
			log.Fatalf("failed to create aes cipger: %v", err)
		}
	}

	return adminConfig, aesCipher
}

func RunApp() {
	var adminConfigFile string
	flag.StringVar(&adminConfigFile, "conf", "", "Dotenv config file for admin server")
	flag.Parse()

	if adminConfigFile == "" {
		log.Fatal("missing flag: -conf config_file")
	}

	adminUserConfig, aesCipher := LoadConfig(adminConfigFile)

	config := &ConfigNewAdmin{}
	config.ListenAddr = utils.HostPortProtocol{IP: adminUserConfig.ListenIP, Port: adminUserConfig.ListenPort}

	config.Table = createStartingTable(&adminUserConfig)
	config.ReadyPlayerName = adminUserConfig.ReadyPlayerName
	config.aesCipher = aesCipher

	admin := NewAdmin(config, &adminUserConfig)

	// Admin REPL
	if adminUserConfig.RunREPL {
		go admin.RunServer()
		admin.RunREPL()
	} else {
		admin.RunServer()
	}
}
