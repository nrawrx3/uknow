package main

import (
	"flag"
	ui "github.com/gizak/termui/v3"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/rksht/uknow"
	"github.com/rksht/uknow/internal/utils"
	client "github.com/rksht/uknow/player_client"
	"log"
	"strings"
)

var configFile string
var envConfig client.EnvConfig

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
	debugFlags := envConfig.GetDebugFlags()

	if !uknow.IsUserNameAllowed(envConfig.PlayerName) {
		log.Fatalf("Only names with alphabet and underscore characters allowed, name given: %s", envConfig.PlayerName)
	}

	// Create the channel used by the client to request to the UI to let the player input a decision
	// command

	commChannels := client.MakeCommChannels()

	table := uknow.NewTable(envConfig.PlayerName)

	playerClientConfig := &client.ConfigNewPlayerClient{
		AskUIForUserTurnChan:       commChannels.AskUIForUserTurnChan,
		DefaultCommandReceiverChan: commChannels.DefaultCommandReceiveChan,
		LogWindowChan:              commChannels.LogWindowChan,
		Table:                      table,
	}

	c := client.NewPlayerClient(playerClientConfig, debugFlags)

	go c.RunServer()
	go c.RunDefaultCommandHandler()

	var uiState client.UIState
	uiState.Init(debugFlags, commChannels.AskUIForUserTurnChan, commChannels.LogWindowChan)
	defer ui.Close()

	uknow.Logger = utils.CreateFileLogger(true, envConfig.PlayerName)

	// uknow.Logger = log.New(&PlayerClientLogger{c: &c}, localPlayerName, log.Lshortfile|log.Ltime)
	go uiState.RunPollInputEvents(commChannels.DefaultCommandReceiveChan)
	uiState.RunDrawLoop()
}

func main() {
	RunApp()
}
