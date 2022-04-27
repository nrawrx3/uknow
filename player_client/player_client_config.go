package client

import (
	"log"

	"github.com/rksht/uknow"
)

type EnvConfig struct {
	CommandListenHost string `split_words:"true" default:"localhost"`
	CommandListenPort int    `split_words:"true" required:"true" split_words:"true"`

	// The default admin address to connect to using `connect_default` command.
	AdminHost  string   `split_words:"true" default:"localhost"`
	AdminPort  int      `split_words:"true" required:"true"`
	PlayerName string   `split_words:"true" required:"true"`
	DebugFlags []string `split_words:"true"`
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
	CardTransferEventChan chan uknow.CardTransferEvent
}

func MakeCommChannels() CommChannels {
	var chans CommChannels
	chans.GeneralUICommandChan = make(chan UICommand)
	chans.AskUIForUserTurnChan = make(chan *UICommandAskUserForDecision)
	chans.NonDecisionReplCommandsChan = make(chan *ReplCommand)
	chans.LogWindowChan = make(chan string, 64) // Logging to ui window doesn't have to be synchronous
	chans.CardTransferEventChan = make(chan uknow.CardTransferEvent)
	return chans
}
