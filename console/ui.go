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
	"os"
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
	uiClear
	uiStop
)

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

	// Used to protect the non-gui state
	stateMutex sync.Mutex

	table *uknow.Table

	rpcClientOfPlayer  map[string]*rpc.Client
	rpcServer          *rpc.Server
	listener           net.Listener
	remoteAddrOfPlayer map[string]string

	// Live connections will keep this wait group alive until all are closed
	liveConnsWaitGroup sync.WaitGroup

	lastCommandFinished chan struct{}
}

type ConsoleLog struct {
	c *Console
}

func (clog *ConsoleLog) Write(bytes []byte) (int, error) {
	clog.c.appendEventLog(string(bytes[:]))
	return len(bytes), nil
}

// The whole application state is in this global.
var c Console

func (c *Console) startServer() {
	c.rpcServer = rpc.NewServer()
	c.rpcServer.RegisterName("Console", &c)
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
				uknow.LogInfo("accept error: %s", err)
			}

			uknow.LogInfo("Received new connection: %s", conn.RemoteAddr)

			c.liveConnsWaitGroup.Add(1)
			go func() {
				c.rpcServer.ServeConn(conn)
				c.liveConnsWaitGroup.Done()
			}()
		}
	}()
}

func (c *Console) executeCommandCell() {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

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
	switch command.Kind {
	case uknow.CmdConnect:
		if c.table.State != uknow.StateBeforeConnect {
			c.appendEventLog("Game already started, cannot connect anymore")
			return nil
		}
		c.appendEventLog(fmt.Sprintf("Connecting to peer: %s", command.ConnectAddress))
		if _, err := c.connectToPeer(command.ConnectAddress); err != nil {
			uknow.LogInfo("%s", err)
			c.appendEventLog(fmt.Sprintf("%s", err))
			return err
		}

	default:
		uknow.LogInfo("Unimplemented command: %s", command)
		c.appendEventLog(fmt.Sprintf("Unimplemented command: %v", command))
	}

	return nil
}

func (c *Console) appendEventLog(line string) {
	c.uiActionCond.L.Lock()
	defer c.uiActionCond.L.Unlock()
	c.eventLogCell.Text = fmt.Sprintf("%s\n%s", c.eventLogCell.Text, line)
	uknow.Logger.Println(line)
	c.uiAction = uiRedraw
	c.uiActionCond.Signal()
}

func (c *Console) appendCommandPrompt(s string) {
	c.commandPromptMutex.Lock()
	defer c.commandPromptMutex.Unlock()
	c.commandStringBeingTyped += s

	uknow.Logger.Printf("c.commandStringBeintTyped = %s\n", c.commandStringBeingTyped)

	c.uiActionCond.L.Lock()
	defer c.uiActionCond.L.Unlock()
	c.commandPromptCell.Text = fmt.Sprintf(" %s_", c.commandStringBeingTyped)
	c.uiAction = uiRedraw
	uknow.Logger.Printf("Set ui action to Redraw\n")
	c.uiActionCond.Signal()
}

func (c *Console) backspaceCommandPrompt() {
	c.commandPromptMutex.Lock()
	defer c.commandPromptMutex.Unlock()
	n := len(c.commandStringBeingTyped)
	if n >= 1 {
		c.commandStringBeingTyped = c.commandStringBeingTyped[0 : n-1]
	}
	c.commandHistoryIndex = maxInt(0, len(c.commandHistory)-1)

	c.uiActionCond.L.Lock()
	c.commandPromptCell.Text = fmt.Sprintf(" %s_", c.commandStringBeingTyped)
	c.uiAction = uiRedraw
	c.uiActionCond.L.Unlock()
	c.uiActionCond.Signal()
}

func (c *Console) resetCommandPrompt(text string, addCurrentTextToHistory bool) {
	c.commandStringBeingTyped = text
	if addCurrentTextToHistory {
		c.commandHistory = append(c.commandHistory, c.commandStringBeingTyped)
		c.commandHistoryIndex = len(c.commandHistory) - 1
	}
	c.uiActionCond.L.Lock()
	c.commandPromptCell.Text = fmt.Sprintf(" %s_", text)
	c.uiAction = uiRedraw
	c.uiActionCond.L.Unlock()
	c.uiActionCond.Signal()
}

func (c *Console) refillHandcountChart() {
	c.uiActionCond.L.Lock()
	defer c.uiActionCond.L.Unlock()
	for i, playerName := range c.table.PlayerNames {
		c.handCountChart.Labels[i] = playerName
		c.handCountChart.Data[i] = float64(len(c.table.HandOfPlayer[playerName]))
	}
	c.uiAction = uiRedraw
	c.uiActionCond.Signal()
}

type RPCBaseArgs struct {
	CallerPlayerName string // name of the caller/replier player
	CallerRemoteAddr string // address at which the caller/replier player is serving RPC requests
}

func (c *Console) getRPCBaseArgs() RPCBaseArgs {
	return RPCBaseArgs{
		CallerPlayerName: c.table.LocalPlayerName,
		CallerRemoteAddr: c.listener.Addr().String(),
	}
}

type AddPlayerArgs struct {
	RPCBaseArgs

	RemoteAddrOfPlayer map[string]string // read by callee to connect to already existing players that it has not connected to
}

type AddPlayerReply struct {
	RPCBaseArgs
	RemoteAddrOfPlayer map[string]string // read by caller to connect to already existing players that it has not connected to
}

// AddPlayer adds the caller player to the game. The callee adds the caller player to its state and replies
// with the currently available players in the cluster so that the caller can call AddPlayer on each of these
// also.
func (c *Console) AddPlayer(args AddPlayerArgs, reply *AddPlayerReply) error {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	_, exists := c.table.IndexOfPlayer[args.CallerPlayerName]
	if exists {
		return fmt.Errorf("Player with name '%s' already added", args.CallerPlayerName)
	}

	client, err := rpc.Dial("tcp", args.CallerRemoteAddr)
	if err != nil {
		err = fmt.Errorf("Failed to connect to caller player in response: %s", err)
		uknow.LogInfo("%s", err)
		return err
	}

	c.rpcClientOfPlayer[args.CallerPlayerName] = client
	c.remoteAddrOfPlayer[args.CallerPlayerName] = args.CallerRemoteAddr
	c.table.AddPlayer(args.CallerPlayerName)

	reply.CallerPlayerName = c.table.LocalPlayerName
	reply.CallerRemoteAddr = c.listener.Addr().String()
	reply.RemoteAddrOfPlayer = c.remoteAddrOfPlayer

	return nil
}

// connectToKnownPlayers is establishes rpc connections with peers that are connected to at least one of the
// servers that the local server asked to be added to
func (c *Console) connectToKnownPlayers(remoteAddrOfPlayer map[string]string) error {
	for playerName, remoteAddr := range remoteAddrOfPlayer {
		if _, exists := c.table.IndexOfPlayer[playerName]; exists {
			continue
		}
		rpcClient, err := rpc.Dial("tcp", remoteAddr)
		if err != nil {
			err = fmt.Errorf("Failed to connect to caller player in response: %s", err)
			return err
		}
		c.rpcClientOfPlayer[playerName] = rpcClient
		c.remoteAddrOfPlayer[playerName] = remoteAddr
		c.table.AddPlayer(playerName)
	}
	return nil
}

func (c *Console) connectToPeer(addr string) (*rpc.Client, error) {
	// Check if we already have a connection open to this peer. This shouldn't happen.
	for _, remoteAddr := range c.remoteAddrOfPlayer {
		if remoteAddr == addr {
			err := fmt.Errorf("Already connected to remote address %s", remoteAddr)
			uknow.LogInfo("%s", err)
			return nil, err
		}
	}

	rpcClient, err := rpc.Dial("tcp", addr)
	if err != nil {
		uknow.Logger.Printf("Failed to connect to remote player at address: %s\n", addr)
		return nil, err
	}

	// Don't know the name of the peer yet. We will obtain it from the reply of the AddPlayer RPC.

	args := AddPlayerArgs{
		c.getRPCBaseArgs(),
		c.remoteAddrOfPlayer,
	}
	var reply AddPlayerReply
	if err := rpcClient.Call("Console.AddPlayer", args, &reply); err != nil {
		uknow.LogInfo("Failed to invoke AddPlayer RPC on peer: %s", err)
		return rpcClient, err
	}

	c.table.AddPlayer(reply.CallerPlayerName)
	c.remoteAddrOfPlayer[reply.CallerPlayerName] = addr
	c.rpcClientOfPlayer[reply.CallerPlayerName] = rpcClient

	c.connectToKnownPlayers(reply.RemoteAddrOfPlayer)
	return rpcClient, nil
}

func init() {
	flag.StringVar(&localPlayerName, "name", "", "Your name")
}

func main() {
	flag.Parse()
	if err := ui.Init(); err != nil {
		log.Fatalf("Failed to initialized termui: %v", err)
	}

	defer ui.Close()

	if localPlayerName == "" {
		log.Fatalf("Need -name=<your_name> flag")
	}

	if !playerNameRegex.MatchString(localPlayerName) {
		log.Fatalf("Only names with alpha-numeric and underscore characters allowed")
	}

	c.uiActionCond = sync.NewCond(&c.uiActionMutex)
	c.uiAction = uiRedraw

	c.table = uknow.NewTable(localPlayerName)

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

	c.startServer()

	uknow.Logger = log.New(os.Stderr, "", log.Ltime)

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
				case "<Resize>":
					c.uiActionCond.L.Lock()
					c.uiAction = uiClear
					payload := e.Payload.(ui.Resize)
					c.grid.SetRect(0, 0, payload.Width, payload.Height)
					c.uiActionCond.L.Unlock()
					c.uiActionCond.Signal()
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
					uknow.Logger.Printf("Event: %v\n", e)
					c.appendCommandPrompt(e.ID)
				}

				// case <-ticker:
				// 	c.uiActionCond.L.Lock()
				// 	c.uiAction = uiRedraw
				// 	c.tableCell.Text = fmt.Sprintf("TickerCount = %d", tickerCount)
				// 	c.uiActionCond.L.Unlock()
				// 	c.uiActionCond.Signal()
				// 	tickerCount++

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
		case uiClear:
			ui.Clear()
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
