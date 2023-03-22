package client

import (
	"github.com/rksht/uknow"
)

type ClientUserConfig struct {
	Type string `json:"type"` // Should always be "client"

	CommandListenIP   string `json:"command_listen_ip"`
	CommandListenPort int    `json:"command_listen_port"`

	AdvertiseIP string `json:"advertise_ip"`

	// The default admin address to connect to using `connect_default` command.
	AdminHostIP     string `json:"admin_host_ip"`
	AdminPort       int    `json:"admin_port"`
	PlayerName      string `json:"player_name"`
	AESKeyString    string `json:"aes_key"`
	EncryptMessages bool   `json:"encrypt_messages"`
}

// Channels used for communication between the client components - PlayerClientUI and PlayerClient
type CommChannels struct {
	GeneralUICommandChan chan UICommand

	// The UI has a goroutine that receives info regarding player turn - and asks the player to input their decision - on this channel
	AskUIForUserTurnChan chan *UICommandAskUserForDecision

	// The PlayerClient receives commands from the UI on this channel.
	NonDecisionReplCommandsChan chan *ReplCommand

	// The UI has a goroutine listening for log strings on this channel.
	LogWindowChan chan string

	// The PlayerClient sends card transfer events on this channel after executing game logic. The PlayerClientUI receives these and updates the UI.
	GameEventChan chan uknow.GameEvent
}

func MakeCommChannels() CommChannels {
	var chans CommChannels
	chans.GeneralUICommandChan = make(chan UICommand)
	chans.AskUIForUserTurnChan = make(chan *UICommandAskUserForDecision)
	chans.NonDecisionReplCommandsChan = make(chan *ReplCommand)
	chans.LogWindowChan = make(chan string, 64) // Logging to ui window doesn't have to be synchronous
	chans.GameEventChan = make(chan uknow.GameEvent)
	return chans
}
