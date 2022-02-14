package client

import (
	"fmt"
	"log"
	"sync"

	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/rksht/uknow"
)

type uiAction int

const (
	uiDrawn uiAction = iota
	uiRedraw
	uiClearRedraw
	uiStop
)

type ClientUIChannels struct {
	generalUICommandChan <-chan UICommand
	askUIForUserTurnChan <-chan *UICommandAskForUserInput

	// Used to print logs into the event log
	logWindowChan <-chan string
}

type ClientUI struct {
	// Signalling the UI process that we have updated UI data is done by the actionCond and concurrent
	// rw is protected by the actionMutex
	actionMutex       sync.Mutex // protects access to every widget object
	actionCond        *sync.Cond // used to signal to UI goro that widget data has been updated and should be drawn
	action            uiAction
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

	ClientUIChannels

	allowDecisionCommands bool

	debugFlags DebugFlags
}

func (clientUI *ClientUI) notifyRedrawUI(action uiAction, exec func()) {
	clientUI.actionCond.L.Lock()
	defer clientUI.actionCond.L.Unlock()
	exec()
	clientUI.action = action
	clientUI.actionCond.Signal()
}

func (clientUI *ClientUI) handleCommandInput(commandChan chan<- uknow.Command) {
	clientUI.commandPromptMutex.Lock()
	defer clientUI.commandPromptMutex.Unlock()
	command, err := uknow.ParseCommandFromInput(clientUI.commandStringBeingTyped)

	if err != nil {
		clientUI.appendEventLog(err.Error())
		return
	}

	if command.Kind.IsUserDecisionCommand() {
		if !clientUI.allowDecisionCommands {
			clientUI.appendEventLog("User decision commands not allowed currently")
			return
		}
		uknow.Logger.Printf("Before sending command to commandChan")
		commandChan <- command
		uknow.Logger.Printf("Done sending command to commandChan")
		clientUI.allowDecisionCommands = false
	} else {
		commandChan <- command
	}

	clientUI.resetCommandPrompt("", true)
}

func (clientUI *ClientUI) logUserDecisionCommand(command uknow.Command, commandChan chan<- uknow.Command) {
	if clientUI.debugFlags.NoAdmin {
		clientUI.appendEventLog(fmt.Sprintf("User decision command: %+v", command))
		commandChan <- command
	}
}

func (clientUI *ClientUI) appendEventLog(line string) {
	clientUI.notifyRedrawUI(uiRedraw, func() {
		clientUI.eventLogCell.Text = fmt.Sprintf("%s\n%s", clientUI.eventLogCell.Text, line)
		// uknow.Logger.Println(line)
	})
}

func (clientUI *ClientUI) appendCommandPrompt(s string) {
	clientUI.commandPromptMutex.Lock()
	defer clientUI.commandPromptMutex.Unlock()
	clientUI.commandStringBeingTyped += s
	clientUI.notifyRedrawUI(uiRedraw, func() {
		clientUI.commandPromptCell.Text = fmt.Sprintf(" %s_", clientUI.commandStringBeingTyped)
	})
}

func (clientUI *ClientUI) backspaceCommandPrompt() {
	clientUI.commandPromptMutex.Lock()
	defer clientUI.commandPromptMutex.Unlock()
	n := len(clientUI.commandStringBeingTyped)
	if n >= 1 {
		clientUI.commandStringBeingTyped = clientUI.commandStringBeingTyped[0 : n-1]
	}
	clientUI.commandHistoryIndex = maxInt(0, len(clientUI.commandHistory)-1)

	clientUI.notifyRedrawUI(uiRedraw, func() {
		clientUI.commandPromptCell.Text = fmt.Sprintf(" %s_", clientUI.commandStringBeingTyped)
	})
}

// DOES NOT LOCK commandPromptMutex
func (clientUI *ClientUI) resetCommandPrompt(text string, addCurrentTextToHistory bool) {
	clientUI.commandStringBeingTyped = text
	if addCurrentTextToHistory {
		clientUI.commandHistory = append(clientUI.commandHistory, clientUI.commandStringBeingTyped)
		clientUI.commandHistoryIndex = len(clientUI.commandHistory) - 1
	}

	clientUI.notifyRedrawUI(uiRedraw, func() {
		clientUI.commandPromptCell.Text = fmt.Sprintf(" %s_", text)
	})
}

// DOES NOT LOCK actionMutex
func (clientUI *ClientUI) refillHandcountChart(table *uknow.Table) {
	playerCount := len(table.PlayerNames)

	if len(clientUI.handCountChart.Labels) < playerCount {
		clientUI.handCountChart.Labels = make([]string, playerCount)
		clientUI.handCountChart.Data = make([]float64, playerCount)
	} else {
		clientUI.handCountChart.Labels = clientUI.handCountChart.Labels[0:playerCount]
		clientUI.handCountChart.Data = clientUI.handCountChart.Data[0:playerCount]
	}

	for i, playerIndex := range table.PlayerIndicesSortedByTurn() {
		playerName := table.PlayerNames[playerIndex]
		clientUI.handCountChart.Labels[i] = playerName
		clientUI.handCountChart.Data[i] = float64(len(table.HandOfPlayer[playerName]))
	}
}

// Creates and initializes the widget structs. All updates to the UI happens via modifying data in these
// structs. So even if we don't have a ui goro running, these structs can be modified anyway - no need to
// check first if ui is disabled or not
func (clientUI *ClientUI) initWidgetObjects() {
	clientUI.tableCell = widgets.NewParagraph()
	clientUI.tableCell.Title = "Table"

	clientUI.handCountChart = widgets.NewBarChart()
	clientUI.handCountChart.Labels = make([]string, 0, 16)
	clientUI.handCountChart.Data = make([]float64, 0, 16)
	clientUI.handCountChart.Title = "Hand count"

	clientUI.drawDeckGauge = widgets.NewGauge()
	clientUI.drawDeckGauge.Percent = 100
	clientUI.drawDeckGauge.BarColor = ui.ColorWhite
	clientUI.drawDeckGauge.Title = "DrawDeck"

	clientUI.eventLogCell = widgets.NewParagraph()
	clientUI.eventLogCell.Title = "Event Log"

	clientUI.commandPromptCell = widgets.NewParagraph()
	clientUI.commandPromptCell.Title = "Command Input"
	clientUI.resetCommandPrompt("", false)

	clientUI.commandHistoryIndex = -1
	clientUI.commandHistory = make([]string, 0, 64)
}

func (clientUI *ClientUI) Init(debugFlags DebugFlags,
	generalUICommandChan <-chan UICommand,
	askUIForUserTurnChan <-chan *UICommandAskForUserInput,
	logWindowChan <-chan string) {
	if err := ui.Init(); err != nil {
		log.Fatalf("Failed to initialized termui: %v", err)
	}

	clientUI.actionCond = sync.NewCond(&clientUI.actionMutex)
	clientUI.action = uiRedraw

	clientUI.initWidgetObjects()

	clientUI.generalUICommandChan = generalUICommandChan
	clientUI.askUIForUserTurnChan = askUIForUserTurnChan

	clientUI.grid = ui.NewGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	clientUI.grid.SetRect(0, 0, termWidth, termHeight)

	clientUI.grid.Set(
		ui.NewRow(0.05, clientUI.drawDeckGauge),
		ui.NewRow(0.8,
			ui.NewCol(0.3, clientUI.tableCell),
			ui.NewCol(0.3, clientUI.handCountChart),
			ui.NewCol(0.4, clientUI.eventLogCell)),
		ui.NewRow(0.1,
			ui.NewCol(1.0, clientUI.commandPromptCell)),
	)

	clientUI.logWindowChan = logWindowChan
	clientUI.debugFlags = debugFlags
}

func (clientUI *ClientUI) RunGeneralUICommandConsumer() {
	for uiCommand := range clientUI.generalUICommandChan {
		switch cmd := uiCommand.(type) {
		case *UICommandSetServedCards:
			clientUI.appendEventLog("Received UICommandSetServedCards")

			clientUI.notifyRedrawUI(uiRedraw, func() {
				clientUI.drawDeckGauge.Percent = cmd.table.DrawDeck.Len()
				clientUI.refillHandcountChart(cmd.table)
			})

		default:
			clientUI.appendEventLog("Unknown UI command")
		}
	}
}

// Runs in own thread
func (clientUI *ClientUI) RunPollInputEvents(defaultCommandReceiverChan chan<- uknow.Command) {
	defer func() {
		uknow.Logger.Printf("Event loop exits\n")
	}()

	uiEvents := ui.PollEvents()

	curCommandReceiverChan := defaultCommandReceiverChan

	for {
		select {
		case e := <-uiEvents:
			// clientUI.appendEventLog(fmt.Sprintf("Event: %+v", e))

			switch e.ID {
			case "<C-c>":
				curCommandReceiverChan <- uknow.NewCommand(uknow.CmdQuit)
				clientUI.notifyRedrawUI(uiStop, func() {})
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				clientUI.notifyRedrawUI(uiRedraw, func() {
					clientUI.grid.SetRect(0, 0, payload.Width, payload.Height)
				})
			case "<Enter>":
				clientUI.handleCommandInput(curCommandReceiverChan)
				curCommandReceiverChan = defaultCommandReceiverChan
			case "<Space>":
				clientUI.appendCommandPrompt(" ")
			case "<Backspace>":
				clientUI.backspaceCommandPrompt()
			case "<Up>":
				clientUI.commandPromptMutex.Lock()
				if clientUI.commandHistoryIndex >= 0 {
					clientUI.commandStringBeingTyped = clientUI.commandHistory[clientUI.commandHistoryIndex]
					clientUI.resetCommandPrompt(clientUI.commandStringBeingTyped, false)
					clientUI.commandHistoryIndex--
				}
				clientUI.commandPromptMutex.Unlock()
			case "<Down>":
				clientUI.commandPromptMutex.Lock()
				if clientUI.commandHistoryIndex >= 0 && clientUI.commandHistoryIndex < len(clientUI.commandHistory)-1 {
					clientUI.commandStringBeingTyped = clientUI.commandHistory[clientUI.commandHistoryIndex]
					clientUI.resetCommandPrompt(clientUI.commandStringBeingTyped, false)
				}
				if clientUI.commandHistoryIndex < len(clientUI.commandHistory)-1 {
					clientUI.commandHistoryIndex++
				}
				clientUI.commandPromptMutex.Unlock()
			default:
				// uknow.Logger.Printf("Event: %v\n", e)
				clientUI.appendCommandPrompt(e.ID)
			}

		case askUIForUserTurnArgs := <-clientUI.askUIForUserTurnChan:
			if askUIForUserTurnArgs.appQuitting {
				clientUI.notifyRedrawUI(uiStop, func() {})
				askUIForUserTurnArgs.receive <- uknow.NewCommand(uknow.CmdQuit)
				return
			}
			clientUI.allowDecisionCommands = true
			curCommandReceiverChan = askUIForUserTurnArgs.receive

		case logMessage := <-clientUI.logWindowChan:
			clientUI.appendEventLog(logMessage)
		}
	}
}

// Runs in own thread.
func (clientUI *ClientUI) RunDrawLoop() {
	ui.Render(clientUI.grid)
	for i := 0; ; i++ {
		clientUI.actionCond.L.Lock()
		for clientUI.action == uiDrawn {
			clientUI.actionCond.Wait()
		}

		if clientUI.action == uiStop {
			clientUI.actionCond.L.Unlock()
			return
		}

		switch clientUI.action {
		case uiStop:
			clientUI.actionCond.L.Unlock()
			return
		case uiClearRedraw:
			ui.Clear()
			ui.Render(clientUI.grid)
			clientUI.action = uiDrawn
		case uiRedraw:
			// uknow.Logger.Printf("Redrawing UI")
			ui.Render(clientUI.grid)
			clientUI.action = uiDrawn
		default:
			uknow.Logger.Fatalf("Invalid action value\n")
		}
		clientUI.actionCond.L.Unlock()
	}
}

type DummyClientUI struct {
	ClientUIChannels
}

func (dummy *DummyClientUI) Init(generalUICommandChan <-chan UICommand, askUIForUserInput <-chan *UICommandAskForUserInput, logWindowChan <-chan string) {
	dummy.generalUICommandChan = generalUICommandChan
	dummy.askUIForUserTurnChan = askUIForUserInput
	dummy.logWindowChan = logWindowChan
}

func (dummy *DummyClientUI) RunAskUIDumper() {
	for args := range dummy.askUIForUserTurnChan {
		if args.appQuitting {
			args.receive <- uknow.NewCommand(uknow.CmdQuit)
			return
		}
	}
}

func (dummy *DummyClientUI) RunWindowLogger() {
	for range dummy.logWindowChan {
	}
}
