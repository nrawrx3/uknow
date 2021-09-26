package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/rksht/uknow"
	"io"
	"log"
)

type CommandPayload struct {
	SenderName   string        `json:"sender_name"`
	Command      uknow.Command `json:"command"`
	IsResponse   bool          `json:"is_response"`
	NeedApproval bool          `json:"need_approval"`
}

func DecodeCommandPayload(r io.Reader) (CommandPayload, error) {
	var body CommandPayload

	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return body, err
	}

	return body, nil
}

type AddNewPlayersMessage struct {
	PlayerNames       []string `json:"player_names"`
	ClientListenAddrs []string `json:"client_listen_addrs"`
}

func (msg *AddNewPlayersMessage) Add(playerName string, clientHost string, clientPort int) *AddNewPlayersMessage {
	if msg.PlayerNames == nil {
		msg.PlayerNames = make([]string, 0, 4)
	}

	if msg.ClientListenAddrs == nil {
		msg.ClientListenAddrs = make([]string, 0, 4)
	}

	msg.PlayerNames = append(msg.PlayerNames, playerName)
	msg.ClientListenAddrs = append(msg.ClientListenAddrs, ConcatHostPort("http", clientHost, clientPort))
	return msg
}

type GetPlayersMessage struct {
	ListenAddrOfPlayer map[string]string `json:"listen_addr_of_player"`
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

func JSONReader(v interface{}) io.Reader {
	var b bytes.Buffer
	err := json.NewEncoder(&b).Encode(v)

	if err != nil {
		log.Fatal(err)
	}
	return &b
}
