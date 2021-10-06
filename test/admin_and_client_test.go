package cmd

import (
	"encoding/json"
	"github.com/rksht/uknow"
	admin "github.com/rksht/uknow/admin"
	"github.com/rksht/uknow/internal/utils"
	client "github.com/rksht/uknow/player_client"
	"net/http"
	"testing"
	"time"
)

type Configs struct {
	clientConfig *client.ConfigNewPlayerClient
	adminConfig  *admin.ConfigNewAdmin
	commChannels client.CommChannels
	table        *uknow.Table
}

type configAndChannels struct {
	clientConfig *client.ConfigNewPlayerClient
	commChannels client.CommChannels
}

func setupClientConfigs(names []string) map[string]configAndChannels {
	clientConfigMap := make(map[string]configAndChannels)

	for i, name := range names {
		commChannels := client.MakeCommChannels()
		clientConfig := &client.ConfigNewPlayerClient{}
		clientConfig.ListenAddr = utils.TCPAddress{Host: "localhost", Port: 9000 + i}
		clientConfig.LogWindowChan = commChannels.LogWindowChan
		clientConfig.AskUIForUserTurnChan = commChannels.AskUIForUserTurnChan
		clientConfig.DefaultCommandReceiverChan = commChannels.DefaultCommandReceiveChan
		clientConfig.Table = uknow.NewTable(name)

		cc := configAndChannels{
			clientConfig: clientConfig,
			commChannels: commChannels,
		}

		clientConfigMap[name] = cc
	}

	return clientConfigMap
}

func setupConfig() *Configs {
	// Create a single client and an admin
	commChannels := client.MakeCommChannels()
	clientConfig := &client.ConfigNewPlayerClient{}
	clientConfig.ListenAddr = utils.TCPAddress{Host: "localhost", Port: 9000}
	clientConfig.LogWindowChan = commChannels.LogWindowChan
	clientConfig.AskUIForUserTurnChan = commChannels.AskUIForUserTurnChan
	clientConfig.DefaultCommandReceiverChan = commChannels.DefaultCommandReceiveChan

	clientConfig.Table = uknow.NewTable("alice")

	adminConfig := &admin.ConfigNewAdmin{}
	adminConfig.State = admin.StatusAddingPlayers
	adminConfig.ListenAddr = utils.TCPAddress{Host: "localhost", Port: 9010}
	adminConfig.Table = uknow.NewAdminTable()

	return &Configs{
		clientConfig: clientConfig,
		adminConfig:  adminConfig,
		commChannels: commChannels,
	}
}

func goRunDummyUI(commChannels client.CommChannels) {
	dummyUI := &client.DummyUIState{}
	dummyUI.Init(commChannels.AskUIForUserTurnChan, commChannels.LogWindowChan)

	go dummyUI.RunAskUIDumper()
	go dummyUI.RunWindowLogger()
}

func TestAddNewPlayer(t *testing.T) {
	configs := setupConfig()

	client := client.NewPlayerClient(configs.clientConfig, client.DebugFlags{})
	admin := admin.NewAdmin(configs.adminConfig)

	goRunDummyUI(configs.commChannels)

	go client.RunServer()
	go admin.RunServer()
	go client.RunDefaultCommandHandler()

	time.Sleep(1 * time.Second)

	t.Log("Done setting up client and admin")

	// Send a connect command to the client default channel. Let it call the admin and add itself to the
	// admin
	cmd := uknow.NewCommand(uknow.CmdConnect)
	cmd.ExtraData = configs.adminConfig.ListenAddr.HTTPAddress()

	t.Log("Sending connect command to client")
	t.Logf("Command receive chan from test: %v", configs.commChannels.DefaultCommandReceiveChan)

	configs.commChannels.DefaultCommandReceiveChan <- cmd

	time.Sleep(1 * time.Second)
}

const aliceName = "alice"
const jackName = "jack"

func TestAddMultiplePlayers(t *testing.T) {
	clientConfigMap := setupClientConfigs([]string{aliceName, jackName})

	adminConfig := &admin.ConfigNewAdmin{}
	adminConfig.State = admin.StatusAddingPlayers
	adminConfig.ListenAddr = utils.TCPAddress{Host: "localhost", Port: 9010}
	adminConfig.Table = uknow.NewAdminTable()

	admin := admin.NewAdmin(adminConfig)
	clients := make(map[string]*client.PlayerClient)

	for name, cc := range clientConfigMap {
		clients[name] = client.NewPlayerClient(cc.clientConfig, client.DebugFlags{})
	}

	for name, cc := range clientConfigMap {
		goRunDummyUI(cc.commChannels)

		client := clients[name]
		go client.RunServer()
		go client.RunDefaultCommandHandler()
	}

	go admin.RunServer()

	t.Log("done setting up admin and clients. waiting 1 sec...")
	time.Sleep(1 * time.Second)

	connectCmd := uknow.NewCommand(uknow.CmdConnect)
	connectCmd.ExtraData = adminConfig.ListenAddr.HTTPAddress()

	// Connect alice to admin
	clientConfigMap[aliceName].commChannels.DefaultCommandReceiveChan <- connectCmd

	t.Log("client alice should connect to admin. checking after 1 sec")
	time.Sleep(1 * time.Second)

	// Connect jack to admin
	clientConfigMap[jackName].commChannels.DefaultCommandReceiveChan <- connectCmd
	t.Log("client jack should connect to admin. checking after 1 sec")
	time.Sleep(1 * time.Second)

	// jack should now have connected to alice also
	jackURL := clientConfigMap[jackName].clientConfig.ListenAddr.HTTPAddress()
	resp, err := http.Get(jackURL + "/players")

	t.Log("Received response from jack")

	if err != nil {
		t.Logf("Failed to GET /players at url: %s", jackURL+"/players")
		t.Log(err)
		t.Fail()
	}

	if resp.StatusCode != http.StatusOK {
		t.Fail()
	}

	var respMessage utils.GetPlayersMessage
	if err := json.NewDecoder(resp.Body).Decode(&respMessage); err != nil {
		t.Log(err)
		t.FailNow()
	}

	t.Logf("alice GET /players response: %+v", respMessage)
}
