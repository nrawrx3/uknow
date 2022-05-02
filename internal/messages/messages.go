package messages

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"

	"github.com/rksht/uknow"
	"github.com/rksht/uknow/internal/utils"
)

type AddNewPlayersMessage struct {
	PlayerNames       []string           `json:"player_names"`
	ClientListenAddrs []utils.TCPAddress `json:"client_listen_addrs"`
}

func (msg *AddNewPlayersMessage) Add(playerName string, clientHost string, clientPort int, protocol string) *AddNewPlayersMessage {
	if msg.PlayerNames == nil {
		msg.PlayerNames = make([]string, 0, 4)
	}

	if msg.ClientListenAddrs == nil {
		msg.ClientListenAddrs = make([]utils.TCPAddress, 0, 4)
	}

	msg.PlayerNames = append(msg.PlayerNames, playerName)
	msg.ClientListenAddrs = append(msg.ClientListenAddrs, utils.TCPAddress{clientHost, clientPort, protocol})
	return msg
}

type GetPlayersMessage struct {
	ListenAddrOfPlayer map[string]utils.TCPAddress `json:"listen_addr_of_player"`
}

type AckNewPlayerAddedMessage struct {
	AckerPlayer string `json:"acker_player"`
	NewPlayer   string `json:"new_player"`
}

// Sent my admin to all players
type SetReadyMessage struct {
	ShufflerName          string `json:"shuffler_name"`
	ShufflerIsFirstPlayer bool   `json:"shuffler_is_first_player"`
}

type EventMessage interface {
	RestPath() string
}

type ServedCardsEvent struct {
	Table uknow.Table `json:"table"`
}

func (*ServedCardsEvent) RestPath() string {
	return "served_cards"
}

type ChosenPlayerEvent struct {
	PlayerName           string `json:"player_name"`
	DecisionEventCounter int    `json:"decision_event_counter"`
}

func (*ChosenPlayerEvent) RestPath() string {
	return "chosen_player"
}

type PlayerDecisionsEvent struct {
	Decisions            []uknow.PlayerDecision `json:"decisions"`
	PlayerName           string                 `json:"player_name"`
	DecisionEventCounter int                    `json:"decision_event_counter"` // counter for tracking/debugging decisions in case of disconnections
}

func (*PlayerDecisionsEvent) RestPath() string {
	return "player_decisions"
}

type PlayerDecisionsSyncEvent struct {
	PlayerDecisionsEvent
}

func (*PlayerDecisionsSyncEvent) RestPath() string {
	return "player_decisions_sync"
}

type UnwrappedErrorPayload struct {
	Errors []string `json:"errors"`
}

func (payload *UnwrappedErrorPayload) Add(err error) {
	if payload.Errors == nil {
		payload.Errors = make([]string, 0, 4)
	}
	payload.Errors = append(payload.Errors, err.Error())
	for {
		err = errors.Unwrap(err)
		if err == nil {
			break
		}
		payload.Errors = append(payload.Errors, err.Error())
	}
}

func WriteErrorPayload(w io.Writer, err error) {
	UnwrappedErrorPayload := UnwrappedErrorPayload{}
	UnwrappedErrorPayload.Add(err)
	json.NewEncoder(w).Encode(&UnwrappedErrorPayload)
}

func MustJSONReader(v interface{}) io.Reader {
	var b bytes.Buffer
	err := json.NewEncoder(&b).Encode(v)

	if err != nil {
		log.Fatal(err)
	}
	return &b
}
