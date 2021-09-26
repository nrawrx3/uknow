package client

import (
	"flag"
	ui "github.com/gizak/termui/v3"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/rksht/uknow"
	"github.com/rksht/uknow/internal/utils"
	"log"
	"strings"
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

func (conf *EnvConfig) debugFlags() DebugFlags {
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

var configFile string

var envConfig EnvConfig

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

func RunApp() {
	flag.StringVar(&configFile, "conf", ".env", "config file")
	flag.Parse()

	if err := godotenv.Load(configFile); err != nil {
		log.Fatal(err.Error())
	}

	err := envconfig.Process("", &envConfig)
	if err != nil {
		log.Fatal(err.Error())
	}

	envConfig.PlayerName = strings.TrimSpace(envConfig.PlayerName)
	debugFlags := envConfig.debugFlags()

	if !uknow.IsUserNameAllowed(envConfig.PlayerName) {
		log.Fatalf("Only names with alphabet and underscore characters allowed, name given: %s", envConfig.PlayerName)
	}

	// Create the channel used by the client to request to the UI to let the player input a decision
	// command

	commChannels := MakeCommChannels()

	table := uknow.NewTable(envConfig.PlayerName)

	playerClientConfig := &ConfigNewPlayerClient{
		AskUIForUserTurnChan:       commChannels.AskUIForUserTurnChan,
		DefaultCommandReceiverChan: commChannels.DefaultCommandReceiveChan,
		LogWindowChan:              commChannels.LogWindowChan,
		Table:                      table,
	}

	c := NewPlayerClient(playerClientConfig, debugFlags)

	go c.RunServer()
	go c.RunDefaultCommandHandler()

	var uiState UIState
	uiState.init(debugFlags, commChannels.AskUIForUserTurnChan, commChannels.LogWindowChan)
	defer ui.Close()

	uknow.Logger = utils.CreateFileLogger(true, envConfig.PlayerName)

	// uknow.Logger = log.New(&PlayerClientLogger{c: &c}, localPlayerName, log.Lshortfile|log.Ltime)
	go uiState.pollInputEvents(commChannels.DefaultCommandReceiveChan)
	uiState.drawLoop()
}
