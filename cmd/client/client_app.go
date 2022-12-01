package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	ui "github.com/gizak/termui/v3"
	"github.com/rksht/uknow"
	cmdcommon "github.com/rksht/uknow/cmd"
	"github.com/rksht/uknow/internal/utils"
	client "github.com/rksht/uknow/player_client"
)

var configFile string

func LoadConfig(configFile string) (client.ClientUserConfig, *uknow.AESCipher) {
	f, err := os.Open(configFile)
	if err != nil {
		log.Fatalf("failed to open config file %s: %v", configFile, err)
	}
	defer f.Close()

	configBytes, err := io.ReadAll(f)
	if err != nil {
		log.Fatalf("failed to read config file %s: %v", configFile, err)
	}

	var clientConfig client.ClientUserConfig
	err = json.NewDecoder(bytes.NewReader(configBytes)).Decode(&clientConfig)
	if err != nil {
		log.Fatalf("failed to parse admin config: %v", err)
	}

	if clientConfig.Type != "client" {
		log.Fatalf("expected \"type\" field in config to have value \"admin\"")
	}

	var aesCipher *uknow.AESCipher
	if clientConfig.EncryptMessages {
		aesCipher, err = uknow.NewAESCipher(clientConfig.AESKeyString)
		if err != nil {
			log.Fatalf("failed to create aes cipger: %v", err)
		}
	}

	return clientConfig, aesCipher
}

func RunApp() {
	// flag.StringVar(&configFile, "conf", ".env", "config file")
	// flag.StringVar(&configPrefix, "conf-prefix", "", "config key prefix")

	flag.StringVar(&configFile, "conf", "", "config file")
	flag.Parse()

	clientConfig, aesCipher := LoadConfig(configFile)

	if !client.IsUserNameAllowed(clientConfig.PlayerName) {
		log.Fatalf("Only names with alphabet and underscore characters allowed, name given: %s", clientConfig.PlayerName)
	}

	tableLogger := uknow.CreateFileLogger(false, fmt.Sprintf("table_%s", clientConfig.PlayerName))
	table := uknow.NewTable(clientConfig.PlayerName, tableLogger)

	// Channels used for comms events, etc.
	commChannels := client.MakeCommChannels()
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
		ListenAddr: utils.HostPortProtocol{
			IP:       clientConfig.CommandListenIP,
			Port:     clientConfig.CommandListenPort,
			Protocol: "http",
		},
		AESCipher: aesCipher,
	}

	if clientConfig.AdminHostIP != "" && clientConfig.AdminPort != 0 {
		playerClientConfig.DefaultAdminAddr = utils.HostPortProtocol{IP: clientConfig.AdminHostIP, Port: clientConfig.AdminPort}
	}

	// FILTHY(@rk):TODO(@rk): Delete this when done with proper implementation in ui
	// client.DummyCardTransferEventConsumerChan = make(chan uknow.CardTransferEvent)

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

	uiLogger := uknow.CreateFileLogger(false, fmt.Sprintf("ui_%s", clientConfig.PlayerName))

	var clientUI client.ClientUI
	clientUI.Init(uiLogger,
		commChannels.GeneralUICommandChan,
		commChannels.AskUIForUserTurnChan,
		commChannels.NonDecisionReplCommandsChan,
		commChannels.GameEventChan,
		commChannels.LogWindowChan)
	defer ui.Close()

	go clientUI.RunPollInputEvents(clientConfig.PlayerName)
	go clientUI.RunGeneralUICommandConsumer(clientConfig.PlayerName)
	go clientUI.RunGameEventProcessor(clientConfig.PlayerName)
	clientUI.RunDrawLoop()
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			ui.Clear()
			ui.Close()
			log.Printf("There was a panic: %v", r)
		}
	}()

	RunApp()
}
