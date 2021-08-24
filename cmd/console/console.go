package main

import (
	"flag"
	"fmt"
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/rksht/uknow"
	"log"
	"net"
	"net/rpc"
	"regexp"
	"sync"
	_ "time"
)

var localPlayerName string

var playerNameRegex = regexp.MustCompile("[a-zA-Z_]+")

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type uiAction int

const (
	uiDrawn uiAction = iota
	uiRedraw
	uiClearRedraw
	uiStop
)

type ClusterMap map[string]string // Map of player name to their public address

func (c ClusterMap) Clone(excludePlayers []string) ClusterMap {
	if excludePlayers == nil {
		excludePlayers = []string{}
	}

	cloned := make(ClusterMap)
	for k, v := range c {
		exclude := false
		for _, e := range excludePlayers {
			if e == k {
				exclude = true
				break
			}
		}
		if !exclude {
			cloned[k] = v
		}
	}
	return cloned
}

// Signalling the UI goro that we have updated UI data is done by the uiActionCond and concurrenct rw is
// protected by the uiActionMutex
type Console struct {
	uiActionMutex     sync.Mutex // protects access to every widget object
	uiActionCond      *sync.Cond // used to signal to UI goro that widget data has been updated and should be drawn
	uiAction          uiAction
	grid              *ui.Grid
	tableCell         *widgets.Paragraph
	eventLogCell      *widgets.Paragraph
	commandPromptCell *widgets.Paragraph
	drawDeckGauge     *widgets.Gauge
	handCountChart    *widgets.BarChart

	commandPromptMutex      sync.Mutex
	commandStringBeingTyped string
	commandHistory          []string
	commandHistoryIndex     int

	stopEventPollChan chan struct{}

	// Used to protect the non-gui state
	stateMutex sync.Mutex

	table *uknow.Table

	rpcClientOfPlayer  map[string]*rpc.Client
	rpcServer          *rpc.Server
	listener           net.Listener
	neighborListenAddr ClusterMap

	// Live connections will keep this wait group alive until all are closed
	liveConnsWaitGroup sync.WaitGroup

	lastCommandFinished chan struct{}
}

func (c *Console) shutdown() {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	c.uiActionCond.L.Lock()
	c.uiAction = uiStop
	c.uiActionCond.Signal()
	c.uiActionCond.L.Unlock()
}

func (c *Console) notifyRedrawUI(uiAction uiAction, exec func()) {
	c.uiActionCond.L.Lock()
	defer c.uiActionCond.L.Unlock()
	c.uiAction = uiAction
	exec()
	c.uiActionCond.Signal()
}

// The whole application state is in this global.
var c Console

func (c *Console) startServer() {
	c.rpcClientOfPlayer = make(map[string]*rpc.Client)
	c.neighborListenAddr = make(map[string]string)

	c.rpcServer = rpc.NewServer()
	if err := c.rpcServer.RegisterName("Console", c); err != nil {
		uknow.Logger.Fatalf("Failed to register Console as an RPC service: %s", err)
	}
	var err error
	c.listener, err = net.Listen("tcp", ":0")
	if err != nil {
		uknow.Logger.Fatal(err)
	}
	c.appendEventLog(fmt.Sprintf("We're listening at: %s", c.listener.Addr()))

	c.liveConnsWaitGroup.Add(1)

	go func() {
		defer c.liveConnsWaitGroup.Done()

		for {
			conn, err := c.listener.Accept()
			if err != nil {
				uknow.LogInfo("Accept error: %s", err)
			}

			uknow.Logger.Printf("%s server received new conn: %s", c.table.LocalPlayerName, conn.RemoteAddr())

			c.liveConnsWaitGroup.Add(1)
			go func() {
				c.rpcServer.ServeConn(conn)
				c.liveConnsWaitGroup.Done()
			}()
		}
	}()
}

func (c *Console) executeCommandCell() {
	c.commandPromptMutex.Lock()
	defer c.commandPromptMutex.Unlock()
	command, err := uknow.ParseCommandFromInput(c.commandStringBeingTyped)

	if err != nil {
		c.appendEventLog(err.Error())
	} else {
		c.executeCommand(command)
		c.resetCommandPrompt("", true)
	}
}

func (c *Console) executeCommand(command uknow.InputCommand) error {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	switch command.Kind {
	case uknow.CmdConnect:
		if c.table.State != uknow.StateBeforeReady {
			c.appendEventLog(fmt.Sprintf("%s - expected game state: %s, have: %s", command.Kind, uknow.StateBeforeReady, c.table.State))
			return nil
		}
		c.appendEventLog(fmt.Sprintf("Connecting to peer: %s", command.ConnectAddress))
		if err := c.connectToPeer(command.ConnectAddress); err != nil {
			uknow.LogInfo("%s", err)
			c.appendEventLog(fmt.Sprintf("%s", err))
			return err
		}

	case uknow.CmdDeclareReady:
		if c.table.State != uknow.StateBeforeReady {
			c.appendEventLog(fmt.Sprintf("%s - expected game state: %s, havbe: %s", command.Kind, uknow.StateBeforeReady, c.table.State))
			return nil
		}

		c.declareGameIsReady()

	case uknow.CmdTableInfo:
		c.printTableInfo()

	default:
		uknow.LogInfo("Unimplemented command: %s", command)
		c.appendEventLog(fmt.Sprintf("Unimplemented command: %v", command))
	}

	return nil
}

func (c *Console) appendEventLog(line string) {
	c.notifyRedrawUI(uiRedraw, func() {
		c.eventLogCell.Text = fmt.Sprintf("%s\n%s", c.eventLogCell.Text, line)
		// uknow.Logger.Println(line)
	})
}

type ConsoleLogger struct {
	c *Console
}

func (cl *ConsoleLogger) Write(p []byte) (int, error) {
	c.appendEventLog(string(p))
	return len(p), nil
}

func (c *Console) appendCommandPrompt(s string) {
	c.commandPromptMutex.Lock()
	defer c.commandPromptMutex.Unlock()
	c.commandStringBeingTyped += s
	// uknow.Logger.Printf("c.commandStringBeintTyped = %s\n", c.commandStringBeingTyped)
	c.notifyRedrawUI(uiRedraw, func() {
		c.commandPromptCell.Text = fmt.Sprintf(" %s_", c.commandStringBeingTyped)
	})
}

func (c *Console) backspaceCommandPrompt() {
	c.commandPromptMutex.Lock()
	defer c.commandPromptMutex.Unlock()
	n := len(c.commandStringBeingTyped)
	if n >= 1 {
		c.commandStringBeingTyped = c.commandStringBeingTyped[0 : n-1]
	}
	c.commandHistoryIndex = maxInt(0, len(c.commandHistory)-1)

	c.notifyRedrawUI(uiRedraw, func() {
		c.commandPromptCell.Text = fmt.Sprintf(" %s_", c.commandStringBeingTyped)
	})
}

// DOES NOT LOCK commandPromptMutex
func (c *Console) resetCommandPrompt(text string, addCurrentTextToHistory bool) {
	c.commandStringBeingTyped = text
	if addCurrentTextToHistory {
		c.commandHistory = append(c.commandHistory, c.commandStringBeingTyped)
		c.commandHistoryIndex = len(c.commandHistory) - 1
	}

	c.notifyRedrawUI(uiRedraw, func() {
		c.commandPromptCell.Text = fmt.Sprintf(" %s_", text)
	})
}

func (c *Console) printTableInfo() {
	handCounts := make(map[string]int)
	for playerName, hand := range c.table.HandOfPlayer {
		handCounts[playerName] = len(hand)
	}

	msg := fmt.Sprintf(`
State:		%s,
Players:	%+v,
Hand counts:	%+v,
DrawDeck count: %+v,
DiscardPile count: %+v`,
		c.table.State,
		c.table.PlayerNames, handCounts, len(c.table.DrawDeck), len(c.table.Pile))
	c.appendEventLog(msg)
	uknow.Logger.Printf(msg)
}

// DOES NOT LOCK stateMutex
func (c *Console) refillHandcountChart() {
	c.notifyRedrawUI(uiRedraw, func() {
		for i, playerName := range c.table.PlayerNames {
			c.handCountChart.Labels[i] = playerName
			c.handCountChart.Data[i] = float64(len(c.table.HandOfPlayer[playerName]))
		}
	})
}

func (c *Console) addNewPlayer(playerName, peerlistenAddr string, rpcClient *rpc.Client) {
	c.neighborListenAddr[playerName] = peerlistenAddr
	c.rpcClientOfPlayer[playerName] = rpcClient
	c.table.AddPlayer(playerName)
}

type RPCBaseArgs struct {
	CallerPlayerName string // name of the caller/replier player
	CallerListenAddr string // address at which the caller/replier player is serving RPC requests
}

func (c *Console) getRPCBaseArgs() RPCBaseArgs {
	return RPCBaseArgs{
		CallerPlayerName: c.table.LocalPlayerName,
		CallerListenAddr: c.listener.Addr().String(),
	}
}

type MeetNewClusterArgs struct {
	RPCBaseArgs
}

type MeetNewClusterReply struct {
	RPCBaseArgs
	neighborAddresses ClusterMap
}

func (c *Console) MeetNewCluster(args MeetNewClusterArgs, reply *MeetNewClusterReply) error {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	if _, alreadyConnected := c.neighborListenAddr[args.CallerPlayerName]; alreadyConnected {
		return fmt.Errorf("already_connected")
	}

	reply.RPCBaseArgs = c.getRPCBaseArgs()
	reply.neighborAddresses = c.neighborListenAddr.Clone(nil)

	// Set up the rpcClient.
	go func() {
		rpcClient, err := rpc.Dial("tcp", args.CallerListenAddr)
		if err != nil {
			uknow.Logger.Printf("Failed to dial %s at address %s", args.CallerPlayerName, args.CallerListenAddr)
			return
		}
		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()
		c.addNewPlayer(args.CallerPlayerName, args.CallerListenAddr, rpcClient)
	}()

	return nil
}

type MeetMeArgs struct {
	RPCBaseArgs
}

type MeetMeReply struct {
	RPCBaseArgs
}

func (c *Console) MeetMe(args MeetMeArgs, reply *MeetMeReply) error {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	if _, exists := c.neighborListenAddr[args.CallerPlayerName]; exists {
		uknow.Logger.Printf("%s: already met player %s", c.table.LocalPlayerName, args.CallerPlayerName)
	}

	go func() {
		rpcClient, err := rpc.Dial("tcp", args.CallerListenAddr)
		if err != nil {
			uknow.Logger.Printf("%s: %s", c.table.LocalPlayerName, err)
		}

		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()
		c.rpcClientOfPlayer[args.CallerPlayerName] = rpcClient
		c.neighborListenAddr[args.CallerPlayerName] = args.CallerListenAddr
		c.table.AddPlayer(args.CallerPlayerName)
	}()

	return nil
}

type MeetEachPlayerOfClusterArgs struct {
	RPCBaseArgs
	ClusterMap ClusterMap
}

type MeetEachPlayerOfClusterReply struct {
	RPCBaseArgs
}

// OPTY: rpcClient.Dial calls can be made to timeout using a channel. connectToPeer also blocks the console
// from serving any other meaningful request.
func (c *Console) connectToPeer(peerListenAddr string) error {
	// First check if already connected.
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	for _, addr := range c.neighborListenAddr {
		if addr == peerListenAddr {
			return fmt.Errorf("Already connected to a player at addr %s", addr)
		}
	}

	rpcClient, err := rpc.Dial("tcp", peerListenAddr)
	if err != nil {
		return fmt.Errorf("Could not connect to player at addr %s", peerListenAddr)
	}

	args := MeetNewClusterArgs{c.getRPCBaseArgs()}
	var reply MeetNewClusterReply
	if err := rpcClient.Call("Console.MeetNewCluster", args, &reply); err != nil {
		return err
	}

	playersInInitialCluster := c.neighborListenAddr.Clone(nil)
	c.addNewPlayer(reply.CallerPlayerName, reply.CallerListenAddr, rpcClient)

	go func() {
		var wg sync.WaitGroup

		// Meet each player in the other cluster
		for playerName, peerListenAddr := range reply.neighborAddresses {
			if playerName == c.table.LocalPlayerName || playerName == reply.CallerPlayerName {
				continue
			}

			wg.Add(1)
			go func(playerName, peerListenAddr string) {
				defer wg.Done()
				rpcClient, err := rpc.Dial("tcp", peerListenAddr)
				if err != nil {
					uknow.Logger.Printf("Failed to dial to player %s addr %s", playerName, peerListenAddr)
					return
				}

				meetMeArgs := MeetMeArgs{c.getRPCBaseArgs()}
				var meetMeReply MeetMeReply
				err = rpcClient.Call("Console.MeetMe", meetMeArgs, &meetMeReply)
				if err != nil {
					uknow.Logger.Printf("Console.MeetMe failed: %s", err)
					return
				}

				c.addNewPlayer(playerName, peerListenAddr, rpcClient)
			}(playerName, peerListenAddr)
		}
		wg.Wait()

		// .. Inform self's neigbors (the ones that it was already connected to before the previous set of
		// RPCs) to connect to the new neighbors

		neighborAddresses := reply.neighborAddresses.Clone(nil)
		neighborAddresses[reply.CallerPlayerName] = reply.CallerListenAddr

		c.stateMutex.Lock()
		for neighborName, rpcClient := range c.rpcClientOfPlayer {
			_, inInitialCluster := playersInInitialCluster[neighborName]
			if inInitialCluster {
				wg.Add(1)
				go func(neighborClient string, rpcClient *rpc.Client) {
					defer wg.Done()
					args := MeetEachPlayerOfClusterArgs{
						RPCBaseArgs: c.getRPCBaseArgs(),
						ClusterMap:  neighborAddresses,
					}
					var reply MeetEachPlayerOfClusterReply

					rpcClient.Call("Console.MeetEachPlayerOfCluster", args, &reply)
				}(neighborName, rpcClient)
			}
		}
		c.stateMutex.Unlock()
		wg.Wait()
	}()

	return nil
}

type GameIsReadyArgs struct {
	RPCBaseArgs
	indexOfPlayerMap map[string]int // Client of the declaring player will randomly settle upon an orientation of the players in the table
}

type GameIsReadyReply struct {
	RPCBaseArgs
}

func (c *Console) GameIsReady(args GameIsReadyArgs, reply *GameIsReadyReply) error {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	if c.table.State != uknow.StateBeforeReady {
		return fmt.Errorf("Invalid state: %s", c.table.State)
	}

	c.table.SetIndexOfPlayer(args.indexOfPlayerMap)
	c.table.State = uknow.StateBeforeShuffle
	reply.RPCBaseArgs = c.getRPCBaseArgs()
	return nil
}

func (c *Console) declareGameIsReady() error {
	shuffledRange := uknow.ShuffleIntRange(0, len(c.table.PlayerNames))
	c.table.RearrangePlayerIndices(shuffledRange)

	args := GameIsReadyArgs{
		RPCBaseArgs:      c.getRPCBaseArgs(),
		indexOfPlayerMap: c.table.IndexOfPlayer,
	}

	var wg sync.WaitGroup

	replies := make([]GameIsReadyReply, c.table.PlayerCount())
	errors := make([]error, c.table.PlayerCount())

	for playerName, playerIndex := range c.table.IndexOfPlayer {
		if playerName == c.table.LocalPlayerName {
			continue
		}

		rpcClient, exists := c.rpcClientOfPlayer[playerName]

		if !exists {
			panic(fmt.Errorf("No rpcClient for player %s", playerName))
		}

		wg.Add(1)
		go func(playerIndex int) {
			err := rpcClient.Call("Console.GameIsReady", args, &replies[playerIndex])
			if err != nil {
				uknow.Logger.Printf("Console.GameIsReady failed for playerName: %s", err)
				errors[playerIndex] = err
			}
			wg.Done()
		}(playerIndex)
	}
	wg.Wait()

	for _, err := range errors {
		if err != nil {
			return err
		}
	}

	c.table.State = uknow.StateBeforeShuffle
	return nil
}

func init() {
	flag.StringVar(&localPlayerName, "name", "", "Your name")
}

// Creates and initializes the widget structs. All updates to the UI happens via modifying data in these
// structs. So even if we don't have a ui goro running, these structs can be modified anyway - no need to
// check first if ui is disabled or not
func (c *Console) initWidgetObjects() {
	c.tableCell = widgets.NewParagraph()
	c.tableCell.Title = "Table"

	c.handCountChart = widgets.NewBarChart()
	c.handCountChart.Labels = make([]string, 0, 16)
	c.handCountChart.Data = make([]float64, 0, 16)
	c.handCountChart.Title = "Hand count"

	c.drawDeckGauge = widgets.NewGauge()
	c.drawDeckGauge.Percent = 100
	c.drawDeckGauge.BarColor = ui.ColorWhite
	c.drawDeckGauge.Title = "DrawDeck"

	c.eventLogCell = widgets.NewParagraph()
	c.eventLogCell.Title = "Event Log"

	c.commandPromptCell = widgets.NewParagraph()
	c.commandPromptCell.Title = "Command Input"
	c.resetCommandPrompt("", false)

	c.commandHistoryIndex = -1
	c.commandHistory = make([]string, 0, 64)
}

func (c *Console) initWidgets() {
	if err := ui.Init(); err != nil {
		log.Fatalf("Failed to initialized termui: %v", err)
	}

	c.uiActionCond = sync.NewCond(&c.uiActionMutex)
	c.uiAction = uiRedraw

	c.initWidgetObjects()

	c.grid = ui.NewGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	c.grid.SetRect(0, 0, termWidth, termHeight)

	c.grid.Set(
		ui.NewRow(0.05, c.drawDeckGauge),
		ui.NewRow(0.8,
			ui.NewCol(0.3, c.tableCell),
			ui.NewCol(0.3, c.handCountChart),
			ui.NewCol(0.4, c.eventLogCell)),
		ui.NewRow(0.1,
			ui.NewCol(1.0, c.commandPromptCell)),
	)
}

func (c *Console) createTableAndStartServer(playerName string) {
	c.table = uknow.NewTable(playerName)
	c.stopEventPollChan = make(chan struct{}, 2) // In unit tests there is no event polling goro so we don't want to block when signalling it to stop
	c.startServer()
}

func main() {
	flag.Parse()
	if localPlayerName == "" {
		log.Fatalf("Need -name=<your_name> flag")
	}
	if !playerNameRegex.MatchString(localPlayerName) {
		log.Fatalf("Only names with alpha-numeric and underscore characters allowed")
	}

	c.initWidgets()
	defer ui.Close()

	uknow.Logger = createFileLogger(true)

	c.createTableAndStartServer(localPlayerName)

	// uknow.Logger = log.New(&ConsoleLogger{c: &c}, localPlayerName, log.Lshortfile|log.Ltime)

	go func() {
		defer func() {
			uknow.Logger.Printf("Event loop exits\n")
		}()

		uiEvents := ui.PollEvents()
		for {
			select {
			case e := <-uiEvents:
				// c.appendEventLog(fmt.Sprintf("Event: %+v", e))

				switch e.ID {
				case "<C-c>":
					c.uiActionCond.L.Lock()
					c.uiAction = uiStop
					c.uiActionCond.L.Unlock()
					c.uiActionCond.Signal()

					c.notifyRedrawUI(uiStop, func() {})
				case "<Resize>":
					payload := e.Payload.(ui.Resize)
					c.uiActionCond.L.Lock()
					c.uiActionCond.L.Unlock()
					c.uiActionCond.Signal()

					c.notifyRedrawUI(uiRedraw, func() {
						c.grid.SetRect(0, 0, payload.Width, payload.Height)
					})
				case "<Enter>":
					c.executeCommandCell()
				case "<Space>":
					c.appendCommandPrompt(" ")
				case "<Backspace>":
					c.backspaceCommandPrompt()
				case "<Up>":
					c.commandPromptMutex.Lock()
					if c.commandHistoryIndex >= 0 {
						c.commandStringBeingTyped = c.commandHistory[c.commandHistoryIndex]
						c.resetCommandPrompt(c.commandStringBeingTyped, false)
						c.commandHistoryIndex--
					}
					c.commandPromptMutex.Unlock()
				case "<Down>":
					c.commandPromptMutex.Lock()
					if c.commandHistoryIndex >= 0 && c.commandHistoryIndex < len(c.commandHistory)-1 {
						c.commandStringBeingTyped = c.commandHistory[c.commandHistoryIndex]
						c.resetCommandPrompt(c.commandStringBeingTyped, false)
					}
					if c.commandHistoryIndex < len(c.commandHistory)-1 {
						c.commandHistoryIndex++
					}
					c.commandPromptMutex.Unlock()
				default:
					// uknow.Logger.Printf("Event: %v\n", e)
					c.appendCommandPrompt(e.ID)
				}

				// case <-ticker:
				// 	c.uiActionCond.L.Lock()
				// 	c.uiAction = uiRedraw
				// 	c.tableCell.Text = fmt.Sprintf("TickerCount = %d", tickerCount)
				// 	c.uiActionCond.L.Unlock()
				// 	c.uiActionCond.Signal()
				// 	tickerCount++
				//
				//
			case <-c.stopEventPollChan:
				return
			}
		}
	}()

	ui.Render(c.grid)
	for i := 0; ; i++ {
		c.uiActionCond.L.Lock()
		for c.uiAction == uiDrawn {
			c.uiActionCond.Wait()
		}

		if c.uiAction == uiStop {
			c.uiActionCond.L.Unlock()
			return
		}

		switch c.uiAction {
		case uiStop:
			c.uiActionCond.L.Unlock()
			return
		case uiClearRedraw:
			ui.Clear()
			ui.Render(c.grid)
			c.uiAction = uiDrawn
		case uiRedraw:
			uknow.Logger.Printf("Redrawing UI")
			ui.Render(c.grid)
			c.uiAction = uiDrawn
		default:
			uknow.Logger.Fatalf("Invalid uiAction value\n")
		}
		c.uiActionCond.L.Unlock()
	}
}
