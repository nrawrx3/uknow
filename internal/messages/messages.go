package messages

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/rksht/uknow"
	"github.com/rksht/uknow/internal/utils"
)

type AddNewPlayersMessage struct {
	PlayerNames       []string                 `json:"player_names"`
	ClientListenAddrs []utils.HostPortProtocol `json:"client_listen_addrs"`
}

func (msg *AddNewPlayersMessage) Add(playerName string, clientHost string, clientPort int, protocol string) *AddNewPlayersMessage {
	if msg.PlayerNames == nil {
		msg.PlayerNames = make([]string, 0, 4)
	}

	if msg.ClientListenAddrs == nil {
		msg.ClientListenAddrs = make([]utils.HostPortProtocol, 0, 4)
	}

	msg.PlayerNames = append(msg.PlayerNames, playerName)
	msg.ClientListenAddrs = append(msg.ClientListenAddrs, utils.HostPortProtocol{IP: clientHost, Port: clientPort, Protocol: protocol})
	return msg
}

type GetPlayersMessage struct {
	ListenAddrOfPlayer map[string]utils.HostPortProtocol `json:"listen_addr_of_player"`
}

type AckNewPlayerAddedMessage struct {
	AckerPlayer string `json:"acker_player"`
	NewPlayer   string `json:"new_player"`
}

// Sent by admin to all players
type SetReadyMessage struct {
	ShufflerName          string `json:"shuffler_name"`
	ShufflerIsFirstPlayer bool   `json:"shuffler_is_first_player"`
}

// Event messages are used to communicate "stuff" between the clients and admin.
// The REST path for events is POST /events/<event_name> for both client and
// admin. These are distinct from uknow.GameEvent.
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

// Wraps a sequence of player decisions from a single player in an event.
type PlayerDecisionsEvent struct {
	Decisions            []uknow.PlayerDecision `json:"decisions"`
	DecidingPlayer       string                 `json:"deciding_player"`
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

// Decrypt given bytes reader if aesCipher is non-nil, and decode resulting JSON
// bytes into given structPointer
func DecryptAndDecodeJSON(structPointer interface{}, input io.Reader, aesCipher *uknow.AESCipher) error {
	if aesCipher == nil {
		return json.NewDecoder(input).Decode(structPointer)
	}

	return aesCipher.MustDecryptJSON(input).Decode(structPointer)
}

// Encode given struct as JSON and encrypt if given aesCipher is non-nil
func EncodeJSONAndEncrypt(inputStructPointer interface{}, output io.Writer, aesCipher *uknow.AESCipher) error {
	if aesCipher == nil {
		if respWriter, ok := output.(http.ResponseWriter); ok {
			respWriter.Header().Add("Content-Type", "application/json")
		}
		return json.NewEncoder(output).Encode(inputStructPointer)
	}

	if respWriter, ok := output.(http.ResponseWriter); ok {
		respWriter.Header().Add("Content-Type", "application/octet-stream")
	}

	_, err := io.Copy(output, aesCipher.MustEncryptJSON(inputStructPointer))
	return err
}
