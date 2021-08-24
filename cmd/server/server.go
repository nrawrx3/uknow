package main

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/rksht/uknow"
	"io"
	"net/http"
)

type AdminState struct {
	Table *uknow.Table
	// Address of player registed on first request
	ListenAddrOfPlayer map[string]string
	Shuffler     string
	State        GameState
}

var admin AdminState

func init() {
	admin.Table = uknow.NewServerTable()
}

type AddPlayerMessage struct {
	ListenPort string `json:listen_port`
}

func addPlayer(w http.ResponseWriter, r *http.Request) {
	if admin.State != StatusAddingPlayers {
		io.WriteString(w, fmt.Sprintf("Not accepting new players, currently in state: %s", admin.State))
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var msg AddPlayerMessage

	err := json.NewDecoder(r.Body).Decode(&msg)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	v := mux.Vars(r)
	playerName := v["playerName"]
	if playerName == "" {
		io.WriteString(w, "Expected a valid player name")
		w.WriteHeader(http.StatusForbidden)
	} else {
		admin.Table.AddPlayer(playerName)
		admin.ListenAddrOfPlayer[playerName] = fmt.Sprintf("%s:%s", r.Host, msg.ListenPort)
		w.WriteHeader(http.StatusOK)
		admin.Shuffler = playerName
	}
}

type GameState string

const (
	StatusAddingPlayers GameState = "status_adding_players"
	StatusShouldShuffle           = "status_should_shuffle"
	StatusRoundStart              = "status_round_start"
)

type UpdateStatusMessage struct {
	Status string `json:status`
}

func updateStatus(w http.ResponseWriter, r *http.Request) {
	var msg UpdateStatusMessage

	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if msg.Status == string(StatusShouldShuffle) {
		// Shuffle among players in the local table and send the hands and the discard pile to each
		// player
		admin.Table.ShuffleDeckAndDistribute(admin.Shuffler)
		admin.State = StatusRoundStart
		admin.ExecuteTurn()
		w.WriteHeader(http.StatusOK)
	}
}

func (admin *AdminState) ExecuteTurn() {
	drawingPlayer := admin.Table.NextPlayerToDraw
	topPileCard, err := admin.Table.Pile.Top()
	if err != nil {
		uknow.Logger.Fatal(err)
	}

	if topPileCard.Number == uknow.CardReverse {
		admin.Table.Direction = admin.Table.Direction * -1
		admin.NotifyReverse()
		admin.ExecuteTurn()
	}
}

func (admin *AdminState) NotifyReverse() {
	for playerName, host := range()
}

func main() {
	r := mux.NewRouter()

	r.Path("/player/{playerName}").Methods("POST").HandlerFunc(addPlayer)

	// PUT status
	r.Path("/game/status").Methods("PUT").HandlerFunc(updateStatus)

	// POST
	r.Path("/deck/draw").Methods("POST").HandlerFunc(playerDeckDraw)

	// POST
	r.Path("/pile/draw").Methods("POST").HandlerFunc(playerPileDraw)

	// PUT
	r.Path("/pile/put").Methods("PUT").HandlerFunc(playerPilePut)
}
