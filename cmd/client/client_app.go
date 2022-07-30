package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	ui "github.com/gizak/termui/v3"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/rksht/uknow"
	cmdcommon "github.com/rksht/uknow/cmd"
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

	if !client.IsUserNameAllowed(envConfig.PlayerName) {
		log.Fatalf("Only names with alphabet and underscore characters allowed, name given: %s", envConfig.PlayerName)
	}

	// Create the channel used by the client to request to the UI to let the player input a decision
	// command

	commChannels := client.MakeCommChannels()

	tableLogger := uknow.CreateFileLogger(false, fmt.Sprintf("table_%s", envConfig.PlayerName))

	table := uknow.NewTable(envConfig.PlayerName, tableLogger)

	clientChannels := client.ClientChannels{
		GeneralUICommandPushChan:       commChannels.GeneralUICommandChan,
		AskUserForDecisionPushChan:     commChannels.AskUIForUserTurnChan,
		NonDecisionReplCommandPullChan: commChannels.NonDecisionReplCommandsChan,
		LogWindowPushChan:              commChannels.LogWindowChan,
		GameEventPushChan:              commChannels.GameEventChan,
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

	commonConfig, err := cmdcommon.LoadCommonConfig()
	if err != nil {
		log.Fatalf("failed to load common config: %v", err)
	}

	if commonConfig.EncryptMessages {
		playerClientConfig.AESCipher, err = uknow.NewAESCipher(commonConfig.AESKey)
		if err != nil {
			log.Fatal(err)
		}
	}

	c := client.NewPlayerClient(playerClientConfig)

	go c.RunServer()
	go c.RunGeneralCommandHandler()

	uiLogger := uknow.CreateFileLogger(false, fmt.Sprintf("ui_%s", envConfig.PlayerName))

	var clientUI client.ClientUI
	clientUI.Init(uiLogger,
		commChannels.GeneralUICommandChan,
		commChannels.AskUIForUserTurnChan,
		commChannels.NonDecisionReplCommandsChan,
		commChannels.GameEventChan,
		commChannels.LogWindowChan)
	defer ui.Close()

	go clientUI.RunPollInputEvents(envConfig.PlayerName)
	go clientUI.RunGeneralUICommandConsumer(envConfig.PlayerName)
	go clientUI.RunGameEventProcessor(envConfig.PlayerName)
	clientUI.RunDrawLoop()
}

func main() {
	RunApp()
}
