package admin

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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
	ReceivedDecisionOfPlayer  AdminState = "received_decision_of_player"
	DoneSyncingPlayerDecision AdminState = "done_syncing_player_decision"
)

type Admin struct {
	table            *uknow.Table
	stateMutex       sync.Mutex
	stateChangedCond *sync.Cond
	state            AdminState

	// Address of player registered on connect command
	listenAddrOfPlayer map[string]utils.TCPAddress
	shuffler           string
	setReadyPlayer     string
	httpClient         *http.Client
	httpServer         *http.Server
	logger             *log.Logger

	// expectingAcksMu         sync.Mutex
	// expectingAcks           []expectedAck
	// newExpectingAckReceived chan expectedAck
	expectedAcksState *expectedAcksState
}

func (admin *Admin) getState() AdminState {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()
	return admin.state
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

	admin.stateChangedCond = sync.NewCond(&admin.stateMutex)

	admin.httpClient = utils.CreateHTTPClient()

	r := mux.NewRouter()

	r.Path("/player").Methods("POST").HandlerFunc(admin.handleAddNewPlayer)
	r.Path("/ack_player_added").Methods("POST").HandlerFunc(admin.handleAckNewPlayerAdded)
	r.Path("/set_ready").Methods("POST").HandlerFunc(admin.handleSetReady)
	r.Path("/test_command").Methods("POST")
	utils.RoutesSummary(r, admin.logger)

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
const perPlayerSyncCommandTimeout = time.Duration(5) * time.Second
const addNewPlayerAckTimeout = time.Duration(10) * time.Second
const askUserToPlayTimeout = time.Duration(20) * time.Second

var errorWaitingForAcks = errors.New("waiting for acks")
var errorInvalidAdminState = errors.New("invalid admin state")
var errorUnknownPlayer = errors.New("unknown player")

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
	var msg utils.AddNewPlayersMessage

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
	var respAddNewPlayersMessage utils.AddNewPlayersMessage
	for playerName, playerListenAddr := range admin.listenAddrOfPlayer {
		if playerName == newPlayerName {
			continue
		}

		admin.logger.Printf("Telling existing player '%s' about '%s'", playerName, newPlayerName)
		addr, err := utils.ResolveTCPAddress(playerListenAddr.String())
		if err != nil {
			admin.logger.Printf("Failed to resolve playerListenAddr. %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			utils.WriteErrorPayload(w, err)
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
	var addPlayerMsg utils.AddNewPlayersMessage
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
				BodyReader: utils.MustJSONReader(&addPlayerMsg),
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

	var reqBody utils.AckNewPlayerAddedMessage
	err := json.NewDecoder(r.Body).Decode(&reqBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		utils.WriteErrorPayload(w, err)
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

// Req: POST /set_ready
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

	if senderPlayerName != admin.setReadyPlayer {
		admin.logger.Printf("player %s not set as setReadyPlayer", senderPlayerName)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	admin.logger.Printf("handleSetReady: called from address: %s", senderAddr.BindString())

	if admin.state != AddingPlayers {
		admin.logger.Printf("Expecting admin state: %s, but have %s", AddingPlayers, admin.state)

		errorResponse := utils.UnwrappedErrorPayload{}
		errorResponse.Add(fmt.Errorf("handleSetReady: Failed due to %w", errorInvalidAdminState))
		json.NewEncoder(w).Encode(errorResponse)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	admin.expectedAcksState.mu.Lock()
	numExpectingAcks := len(admin.expectedAcksState.list)

	if numExpectingAcks != 0 {
		admin.expectedAcksState.mu.Unlock()
		admin.logger.Printf("handleSetReady: cannot change to ready state, numExpectingAcks = %d (!= 0)", numExpectingAcks)

		errorResponse := utils.UnwrappedErrorPayload{}
		errorResponse.Add(fmt.Errorf("handleSetReady: %w, numExpectingAcks: %d", errorWaitingForAcks, numExpectingAcks))
		json.NewEncoder(w).Encode(errorResponse)
		w.WriteHeader(http.StatusSeeOther)
	}

	admin.expectedAcksState.mu.Unlock()

	var setReadyMessage utils.SetReadyMessage
	err = json.NewDecoder(r.Body).Decode(&setReadyMessage)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		utils.WriteErrorPayload(w, err)
		return
	}

	admin.state = ReadyToServeCards

	// serve cards and serve
	admin.table.ShufflerName = setReadyMessage.ShufflerName
	admin.table.ShuffleDeckAndDistribute()

	if setReadyMessage.ShufflerIsFirstPlayer {
		admin.table.NextPlayerToDraw = admin.table.ShufflerName
	}

	go admin.syncTableWithAllPlayers("serving")

	w.WriteHeader(http.StatusOK)
}

func (admin *Admin) syncTableWithAllPlayers(syncReason string) {
}

func (admin *Admin) playerCommandURL(playerName string) string {
	listenAddr := admin.listenAddrOfPlayer[playerName]
	return listenAddr.HTTPAddress() + "/command"
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
	SetReadyPlayer string `split_words:"true", required:"true"`
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
		}

		_, err = uknow.ParseCommandFromInput(strings.TrimSpace(line))
		if err != nil {
			admin.logger.Print(err)
		}

		if line == "set_ready" || line == "sr" {
			admin.setReady()
		}
	}
}

func (admin *Admin) executeCommand(adminCommand uknow.Command) {
	switch adminCommand.Kind {
	case uknow.CmdConnect: // **Only testing, not actually going to use this command**
		// Ping all players
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
