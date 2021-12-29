package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"

	"github.com/rksht/uknow"
)

type CommandPayload struct {
	SenderName string        `json:"sender_name"`
	Command    uknow.Command `json:"command"`
	IsResponse bool          `json:"is_response"`

	// For user decision commands, the admin is responsible for validating the play made by the player-client.
	// NeedApproval is set to true for such cases. After the user decides the play, the player client
	// waits until it gets approval from the admin.
	NeedApproval bool `json:"need_approval"`
}

func DecodeCommandPayload(r io.Reader) (CommandPayload, error) {
	var body CommandPayload

	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return body, err
	}

	return body, nil
}

type AddNewPlayersMessage struct {
	PlayerNames       []string     `json:"player_names"`
	ClientListenAddrs []TCPAddress `json:"client_listen_addrs"`
}

func (msg *AddNewPlayersMessage) Add(playerName string, clientHost string, clientPort int) *AddNewPlayersMessage {
	if msg.PlayerNames == nil {
		msg.PlayerNames = make([]string, 0, 4)
	}

	if msg.ClientListenAddrs == nil {
		msg.ClientListenAddrs = make([]TCPAddress, 0, 4)
	}

	msg.PlayerNames = append(msg.PlayerNames, playerName)
	msg.ClientListenAddrs = append(msg.ClientListenAddrs, TCPAddress{clientHost, clientPort})
	return msg
}

type GetPlayersMessage struct {
	ListenAddrOfPlayer map[string]TCPAddress `json:"listen_addr_of_player"`
}

type AckNewPlayerAddedMessage struct {
	ConnectingPlayer string `json:"connecting_player"`
	ConnectedPlayer  string `json:"connected_player"`
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
