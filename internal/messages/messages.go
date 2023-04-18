package messages

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/nrawrx3/uknow"
	"github.com/nrawrx3/uknow/internal/utils"
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

// Event messages are what the admin sends to the client.
type ServerEvent interface {
	EventType() EventType
}

type EventType string

const (
	EventTypePlayerJoined        EventType = "player_joined"
	EventTypeExistingPlayersList EventType = "existing_players_list"
	EventTypeServedCards         EventType = "served_cards"
	EventTypeChosenPlayer        EventType = "chosen_player"
	EventTypePlayerDecisionsSync EventType = "player_decisions_sync"
)

type ServerEventMessage struct {
	Type  EventType   `json:"type"`
	Event ServerEvent `json:"event"`
}

func DecodeEvent[T ServerEvent](in []byte) (T, error) {
	var out struct {
		Event T `json:"event"`
	}

	err := json.NewDecoder(bytes.NewReader(in)).Decode(&out)
	if err != nil {
		log.Printf("received %s decoded: %+v", in, out)
	}
	return out.Event, err
}

// Meant to be used in client sse
func ParseServerEventMessage(b []byte) (ServerEvent, error) {
	// Treat as {"type": ...} to get the event type first and then use the proper output type
	var onlyType struct{ Type EventType }
	err := json.NewDecoder(bytes.NewReader(b)).Decode(&onlyType)
	if err != nil {
		return nil, fmt.Errorf("could not parse event type: %w", err)
	}

	switch onlyType.Type {
	case EventTypePlayerJoined:
		return DecodeEvent[PlayerJoinedEvent](b)
	case EventTypeExistingPlayersList:
		return DecodeEvent[ExistingPlayersListEvent](b)
	case EventTypeServedCards:
		return DecodeEvent[ServedCardsEvent](b)
	case EventTypeChosenPlayer:
		return DecodeEvent[ChosenPlayerEvent](b)
	case EventTypePlayerDecisionsSync:
		return DecodeEvent[PlayerDecisionsSyncEvent](b)
	}
	return nil, fmt.Errorf("unknown event, could not parse type: %v", onlyType.Type)
}

// As opposed to AddNewPlayerMessage, we're sending this as SSE event so want a
// message that implements EventMessage
type PlayerJoinedEvent struct {
	PlayerName string `json:"player_name"`
}

type ExistingPlayersListEvent struct {
	PlayerNames []string `json:"player_names"`
}

type ServedCardsEvent struct {
	Table uknow.Table `json:"table"`
}

type ChosenPlayerEvent struct {
	PlayerName           string `json:"player_name"`
	DecisionEventCounter int    `json:"decision_event_counter"`
}

type PlayerDecisionsSyncEvent struct {
	PlayerDecisionsRequest
}

func (PlayerJoinedEvent) EventType() EventType        { return EventTypePlayerJoined }
func (ExistingPlayersListEvent) EventType() EventType { return EventTypeExistingPlayersList }
func (ServedCardsEvent) EventType() EventType         { return EventTypeServedCards }
func (ChosenPlayerEvent) EventType() EventType        { return EventTypeChosenPlayer }
func (PlayerDecisionsSyncEvent) EventType() EventType { return EventTypePlayerDecisionsSync }

// Wraps a sequence of player decisions from a single player in an event.
type PlayerDecisionsRequest struct {
	Decisions            []uknow.PlayerDecision `json:"decisions"`
	DecidingPlayer       string                 `json:"deciding_player"`
	DecisionEventCounter int                    `json:"decision_event_counter"` // counter for tracking/debugging decisions in case of disconnections
}

func (*PlayerDecisionsRequest) RestPath() string {
	return "player_decisions"
}

// TODO: Don't really need this. Simple error codes and/or error messages should
// be fine.
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
