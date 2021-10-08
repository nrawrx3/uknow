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

type expectingPlayerAddedAck struct {
	connectingPlayer string
	connecteePlayer  string
	deadline         time.Time
}

type Admin struct {
	table      *uknow.Table
	stateMutex sync.Mutex

	// Address of player registed on first request
	listenAddrOfPlayer      map[string]utils.TCPAddress
	shuffler                string
	state                   GameState
	httpClient              *http.Client
	httpServer              *http.Server
	logger                  *log.Logger
	expectingAcks           []expectingPlayerAddedAck
	newExpectingAckReceived chan expectingPlayerAddedAck
}

type ConfigNewAdmin struct {
	ListenAddr utils.TCPAddress
	State      GameState
	Table      *uknow.Table
}

func NewAdmin(config *ConfigNewAdmin) *Admin {
	admin := &Admin{
		table:              uknow.NewAdminTable(),
		listenAddrOfPlayer: make(map[string]utils.TCPAddress),
		shuffler:           "",
		state:              StatusAddingPlayers,
		expectingAcks:      make([]expectingPlayerAddedAck, 0, 64),
		logger:             utils.CreateFileLogger(false, "uknow_admin"),
	}

	admin.httpClient = utils.CreateHTTPClient()

	r := mux.NewRouter()

	r.Path("/player").Methods("POST").HandlerFunc(admin.handleAddNewPlayer)
	r.Path("/ack_player_added").Methods("POST").HandlerFunc(admin.handleAckNewPlayerAdded)
	r.Path("/test_command").Methods("POST")
	utils.RoutesSummary(r, admin.logger)

	admin.httpServer = &http.Server{
		Handler:           r,
		Addr:              config.ListenAddr.String(),
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       1 * time.Minute,
		ReadHeaderTimeout: 2 * time.Second,
	}

	return admin
}

func (admin *Admin) RunServer() {
	admin.logger.Printf("Running admin server at addr: %s", admin.httpServer.Addr)
	go admin.runExpectingPlayersCheck()
	err := admin.httpServer.ListenAndServe()
	if err != nil {
		log.Fatalf("Admin.RunServer() failed: %s", err.Error())
	}
}

const allPlayersSyncCommandTimeout = time.Duration(10) * time.Second
const perPlayerSyncCommandTimeout = time.Duration(5) * time.Second

var errorWaitingForAcks = errors.New("waiting for acks")
var errorTimeoutAcks = errors.New("some acks timed out")

// Req:		POST /player AddNewPlayerMessage
// Resp:	AddNewPlayerMessage
func (admin *Admin) handleAddNewPlayer(w http.ResponseWriter, r *http.Request) {
	admin.logger.Printf("addNewPlayer...")

	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	if admin.state != StatusAddingPlayers {
		fmt.Fprintf(w, "Not accepting new players, currently in state: %s", admin.state)
		w.WriteHeader(http.StatusForbidden)
		return
	}

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

	err = admin.table.AddPlayer(newPlayerName)
	if errors.Is(err, uknow.PlayerAlreadyExists) {
		w.WriteHeader(http.StatusOK)
		admin.logger.Printf("player %s already exists", newPlayerName)
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		admin.logger.Fatalf("Cannot add new player: %s", err)
	}

	admin.listenAddrOfPlayer[newPlayerName] = newPlayerListenAddr
	admin.shuffler = newPlayerName

	// Tell existing players about the new player
	newPlayerHost, newPlayerPort, err := utils.ResolveTCPAddress(newPlayerListenAddr.String())
	if err != nil {
		admin.logger.Printf("Invalid newPlayerHostAddr: %s", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	admin.logger.Printf("newPlayerName = %s, newPlayerHost = %s, newPlayerPort = %d", newPlayerName, newPlayerHost, newPlayerPort)

	go admin.tellExistingPlayersAboutNew(context.Background(), newPlayerName, newPlayerHost, newPlayerPort)

	// Tell the new player about existing players
	var responseMsg utils.AddNewPlayersMessage
	for playerName, playerListenAddr := range admin.listenAddrOfPlayer {
		if playerName == newPlayerName {
			continue
		}

		admin.logger.Printf("Telling '%s' at %s about new player %s", playerName, playerListenAddr.String(), newPlayerName)
		host, port, err := utils.ResolveTCPAddress(playerListenAddr.String())
		if err != nil {
			admin.logger.Printf("Failed to resolve playerListenAddr. %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			utils.WriteErrorPayload(w, err)
			continue
		}
		responseMsg.Add(playerName, host, port)
	}

	admin.logger.Printf("Telling %s about existing players: %+v", newPlayerName, responseMsg)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(responseMsg)
}

func (admin *Admin) tellExistingPlayersAboutNew(ctx context.Context, newPlayerName, newPlayerHost string, newPlayerPort int) {
	var addPlayerMsg utils.AddNewPlayersMessage
	addPlayerMsg.Add(newPlayerName, newPlayerHost, newPlayerPort)

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
			url := playerListenAddr.HTTPAddress() + "/player"
			req, err := http.NewRequestWithContext(ctx, "POST", url, utils.JSONReader(&addPlayerMsg))
			if err != nil {
				return err
			}

			_, err = admin.httpClient.Do(req)
			if err != nil {
				return err
			}

			admin.stateMutex.Lock()
			admin.expectingAcks = append(admin.expectingAcks, expectingPlayerAddedAck{
				connectingPlayer: playerName,
				connecteePlayer:  newPlayerName,
				deadline:         time.Now().Add(10 * time.Second),
			})
			admin.stateMutex.Unlock()

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
	admin.logger.Printf("TODO: handleAckNewPlayerAdded")
}

func (admin *Admin) runExpectingPlayersCheck() {
	for ack := range admin.newExpectingAckReceived {
		admin.stateMutex.Lock()

		for i, existingAck := range admin.expectingAcks {
			if existingAck.connectingPlayer == ack.connectingPlayer && existingAck.connecteePlayer == ack.connecteePlayer {
				admin.expectingAcks = append(admin.expectingAcks[0:i], admin.expectingAcks[(i+1):len(admin.expectingAcks)]...)
			}
		}

		admin.stateMutex.Unlock()
	}
}

func (admin *Admin) setReady() error {
	admin.stateMutex.Lock()
	defer admin.stateMutex.Unlock()

	timeoutAcks := make([]expectingPlayerAddedAck, 0)

	for _, existingAck := range admin.expectingAcks {
		if time.Now().After(existingAck.deadline) {
			timeoutAcks = append(timeoutAcks, existingAck)
		}
	}

	if len(timeoutAcks) > 0 {
		return errorTimeoutAcks
	}

	if len(admin.expectingAcks) > 0 {
		return errorWaitingForAcks
	}

	admin.state = StatusShouldShuffle

	return nil
}

type GameState string

const (
	StatusAddingPlayers GameState = "status_adding_players"
	StatusShouldShuffle           = "status_should_shuffle"
	StatusRoundStart              = "status_round_start"
)

// func (admin *Admin) ExecuteTurn() {
// 	topPileCard, err := admin.table.Pile.Top()
// 	if err != nil {
// 		admin.logger.Fatal(err)
// 	}

// 	if topPileCard.Number == uknow.CardReverse {
// 		admin.table.Direction = admin.table.Direction * -1
// 		admin.NotifyReverse()
// 		admin.ExecuteTurn()
// 	}
// }

// func (admin *Admin) NotifyReverse() {
// }

type EnvConfig struct {
	ListenAddr string `envconfig:"ADMIN_LISTEN_ADDR" required:"true"`
	ListenPort int    `envconfig:"ADMIN_LISTEN_PORT" required:"true"`
	RunREPL    bool   `envconfig:"ADMIN_RUN_REPL" required:"false" default:"true"`
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

		_, err = uknow.ParseCommandFromInput(strings.TrimSpace(line))
		if err != nil {
			admin.logger.Print(err)
		}

		if line == "set_ready" || line == "sr" {
			err := admin.setReady()
			if err != nil {
				fmt.Print(err)
			}
		}
	}
}

func (admin *Admin) executeCommand(adminCommand uknow.Command) {
	switch adminCommand.Kind {
	case uknow.CmdConnect: // **Only testing, not actually going to use this command**
		// Ping all players
	}
}

func RunAPP() {
	var envConfig EnvConfig
	var adminConfigFile string
	flag.StringVar(&adminConfigFile, "conf", ".env", "Dotenv config file for admin server")

	flag.Parse()

	err := godotenv.Load(adminConfigFile)
	if err != nil {
		log.Fatal(err.Error())
	}

	err = envconfig.Process("", &envConfig)
	if err != nil {
		log.Fatal(err.Error())
	}

	config := &ConfigNewAdmin{}
	config.ListenAddr = utils.TCPAddress{Host: envConfig.ListenAddr, Port: envConfig.ListenPort}
	config.State = StatusAddingPlayers
	config.Table = uknow.NewAdminTable()

	admin := NewAdmin(config)

	go admin.RunServer()

	// Admin REPL
	if envConfig.RunREPL {
		go admin.RunREPL()
	}
}
