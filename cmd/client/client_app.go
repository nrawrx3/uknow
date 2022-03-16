package main

import (
	"flag"
	"log"
	"strings"

	ui "github.com/gizak/termui/v3"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/rksht/uknow"
	"github.com/rksht/uknow/internal/utils"
	client "github.com/rksht/uknow/player_client"
)

var configFile string
var envConfig client.EnvConfig
var configPrefix string

func RunApp() {
	flag.StringVar(&configFile, "conf", ".env", "config file")
	flag.StringVar(&configPrefix, "conf-prefix", "", "config key prefix")
	flag.Parse()

	if configPrefix == "" {
		log.Print("Value -conf-prefix not specified")
	}

	log.Printf("Loading configs from file %s", configFile)

	if err := godotenv.Load(configFile); err != nil {
		log.Fatal(err.Error())
	}

	err := envconfig.Process(configPrefix, &envConfig)
	if err != nil {
		log.Fatal(err.Error())
	}

	envConfig.PlayerName = strings.TrimSpace(envConfig.PlayerName)
	debugFlags := envConfig.GetDebugFlags()

	if !client.IsUserNameAllowed(envConfig.PlayerName) {
		log.Fatalf("Only names with alphabet and underscore characters allowed, name given: %s", envConfig.PlayerName)
	}

	// Create the channel used by the client to request to the UI to let the player input a decision
	// command

	commChannels := client.MakeCommChannels()

	table := uknow.NewTable(envConfig.PlayerName)

	clientChannels := client.ClientChannels{
		GeneralUICommandPushChan:       commChannels.GeneralUICommandChan,
		AskUserForDecisionPushChan:     commChannels.AskUIForUserTurnChan,
		NonDecisionReplCommandPullChan: commChannels.NonDecisionReplCommandsChan,
		LogWindowPushChan:              commChannels.LogWindowChan,
	}

	playerClientConfig := &client.ConfigNewPlayerClient{
		ClientChannels: clientChannels,
		Table:          table,
		ListenAddr: utils.TCPAddress{
			Host:     envConfig.CommandListenHost,
			Port:     envConfig.CommandListenPort,
			Protocol: "http",
		},
	}

	if envConfig.AdminHost != "" && envConfig.AdminPort != 0 {
		playerClientConfig.DefaultAdminAddr = utils.TCPAddress{Host: envConfig.AdminHost, Port: envConfig.AdminPort}
	}

	// FILTHY(@rk):TODO(@rk): Delete this when done with proper implementation in ui
	client.DummyCardTransferEventConsumerChan = make(chan uknow.CardTransferEvent)

	c := client.NewPlayerClient(playerClientConfig, debugFlags)

	go c.RunServer()
	go c.RunGeneralCommandHandler()

	var clientUI client.ClientUI
	clientUI.Init(debugFlags,
		commChannels.GeneralUICommandChan,
		commChannels.AskUIForUserTurnChan,
		commChannels.NonDecisionReplCommandsChan,
		commChannels.LogWindowChan)
	defer ui.Close()

	uknow.Logger = c.Logger

	go clientUI.RunPollInputEvents(envConfig.PlayerName)
	go clientUI.RunGeneralUICommandConsumer()
	clientUI.RunDrawLoop()
}

func main() {
	RunApp()
}
