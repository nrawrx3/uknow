package client

import (
	"github.com/rksht/uknow"
	"log"
)

type EnvConfig struct {
	CommandListenHost string   `split_words:"true" default:"localhost"`
	CommandListenPort int      `required:"true" split_words:"true"`
	AdminHost         string   `split_words:"true" default:"localhost"`
	AdminPort         int      `split_words:"true" required:"true"`
	PlayerName        string   `split_words:"true" required:"true"`
	DebugFlags        []string `split_words:"true"`
}

type DebugFlags struct {
	NoAdmin      bool
	DummyUILogic bool
}

func (conf *EnvConfig) GetDebugFlags() DebugFlags {
	var flags DebugFlags
	for _, flg := range conf.DebugFlags {
		switch flg {
		case "NO_ADMIN":
			flags.NoAdmin = true
		default:
			log.Fatalf("Unrecognized flag: %s", flg)
		}
	}
	return flags
}

const defaultCommandServerHost = "localhost"

type CommChannels struct {
	AskUIForUserTurnChan      chan askUIForUserTurnArgs
	DefaultCommandReceiveChan chan uknow.Command
	LogWindowChan             chan string
}

func MakeCommChannels() CommChannels {
	var chans CommChannels
	chans.AskUIForUserTurnChan = make(chan askUIForUserTurnArgs)
	chans.DefaultCommandReceiveChan = make(chan uknow.Command)
	chans.LogWindowChan = make(chan string, 64) // Logging to ui window doesn't have to be synchronous
	return chans
}
