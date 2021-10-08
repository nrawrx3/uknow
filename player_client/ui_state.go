package client

import (
	"fmt"
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/rksht/uknow"
	"log"
	"sync"
)

type uiAction int

const (
	uiDrawn uiAction = iota
	uiRedraw
	uiClearRedraw
	uiStop
)

type UIState struct {
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

	askUIForUserTurnChan  <-chan askUIForUserTurnArgs
	allowDecisionCommands bool

	// Used to print logs into the event log
	logWindowChan <-chan string

	debugFlags DebugFlags
}

func (uiState *UIState) notifyRedrawUI(action uiAction, exec func()) {
	uiState.actionCond.L.Lock()
	defer uiState.actionCond.L.Unlock()
	exec()
	uiState.action = action
	uiState.actionCond.Signal()
}

func (uiState *UIState) handleCommandInput(commandChan chan<- uknow.Command) {
	uiState.commandPromptMutex.Lock()
	defer uiState.commandPromptMutex.Unlock()
	command, err := uknow.ParseCommandFromInput(uiState.commandStringBeingTyped)

	if err != nil {
		uiState.appendEventLog(err.Error())
		return
	}

	if command.Kind.IsUserDecisionCommand() {
		if !uiState.allowDecisionCommands {
			uiState.appendEventLog("User decision commands not allowed currently")
			return
		}
		uknow.Logger.Printf("Before sending command to commandChan")
		commandChan <- command
		uknow.Logger.Printf("Done sending command to commandChan")
		uiState.allowDecisionCommands = false
	} else {
		commandChan <- command
	}

	uiState.resetCommandPrompt("", true)
}

func (uiState *UIState) logUserDecisionCommand(command uknow.Command, commandChan chan<- uknow.Command) {
	if uiState.debugFlags.NoAdmin {
		uiState.appendEventLog(fmt.Sprintf("User decision command: %+v", command))
		commandChan <- command
	}
}

func (uiState *UIState) appendEventLog(line string) {
	uiState.notifyRedrawUI(uiRedraw, func() {
		uiState.eventLogCell.Text = fmt.Sprintf("%s\n%s", uiState.eventLogCell.Text, line)
		// uknow.Logger.Println(line)
	})
}

func (uiState *UIState) appendCommandPrompt(s string) {
	uiState.commandPromptMutex.Lock()
	defer uiState.commandPromptMutex.Unlock()
	uiState.commandStringBeingTyped += s
	uiState.notifyRedrawUI(uiRedraw, func() {
		uiState.commandPromptCell.Text = fmt.Sprintf(" %s_", uiState.commandStringBeingTyped)
	})
}

func (uiState *UIState) backspaceCommandPrompt() {
	uiState.commandPromptMutex.Lock()
	defer uiState.commandPromptMutex.Unlock()
	n := len(uiState.commandStringBeingTyped)
	if n >= 1 {
		uiState.commandStringBeingTyped = uiState.commandStringBeingTyped[0 : n-1]
	}
	uiState.commandHistoryIndex = maxInt(0, len(uiState.commandHistory)-1)

	uiState.notifyRedrawUI(uiRedraw, func() {
		uiState.commandPromptCell.Text = fmt.Sprintf(" %s_", uiState.commandStringBeingTyped)
	})
}

// DOES NOT LOCK commandPromptMutex
func (uiState *UIState) resetCommandPrompt(text string, addCurrentTextToHistory bool) {
	uiState.commandStringBeingTyped = text
	if addCurrentTextToHistory {
		uiState.commandHistory = append(uiState.commandHistory, uiState.commandStringBeingTyped)
		uiState.commandHistoryIndex = len(uiState.commandHistory) - 1
	}

	uiState.notifyRedrawUI(uiRedraw, func() {
		uiState.commandPromptCell.Text = fmt.Sprintf(" %s_", text)
	})
}

func (uiState *UIState) refillHandcountChart(table *uknow.Table) {
	uiState.notifyRedrawUI(uiRedraw, func() {
		for i, playerName := range table.PlayerNames {
			uiState.handCountChart.Labels[i] = playerName
			uiState.handCountChart.Data[i] = float64(len(table.HandOfPlayer[playerName]))
		}
	})
}

// Creates and initializes the widget structs. All updates to the UI happens via modifying data in these
// structs. So even if we don't have a ui goro running, these structs can be modified anyway - no need to
// check first if ui is disabled or not
func (uiState *UIState) initWidgetObjects() {
	uiState.tableCell = widgets.NewParagraph()
	uiState.tableCell.Title = "Table"

	uiState.handCountChart = widgets.NewBarChart()
	uiState.handCountChart.Labels = make([]string, 0, 16)
	uiState.handCountChart.Data = make([]float64, 0, 16)
	uiState.handCountChart.Title = "Hand count"

	uiState.drawDeckGauge = widgets.NewGauge()
	uiState.drawDeckGauge.Percent = 100
	uiState.drawDeckGauge.BarColor = ui.ColorWhite
	uiState.drawDeckGauge.Title = "DrawDeck"

	uiState.eventLogCell = widgets.NewParagraph()
	uiState.eventLogCell.Title = "Event Log"

	uiState.commandPromptCell = widgets.NewParagraph()
	uiState.commandPromptCell.Title = "Command Input"
	uiState.resetCommandPrompt("", false)

	uiState.commandHistoryIndex = -1
	uiState.commandHistory = make([]string, 0, 64)
}

func (uiState *UIState) Init(debugFlags DebugFlags, askUIForUserTurnChan <-chan askUIForUserTurnArgs, logWindowChan <-chan string) {
	if err := ui.Init(); err != nil {
		log.Fatalf("Failed to initialized termui: %v", err)
	}

	uiState.actionCond = sync.NewCond(&uiState.actionMutex)
	uiState.action = uiRedraw

	uiState.initWidgetObjects()

	uiState.askUIForUserTurnChan = askUIForUserTurnChan

	uiState.grid = ui.NewGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	uiState.grid.SetRect(0, 0, termWidth, termHeight)

	uiState.grid.Set(
		ui.NewRow(0.05, uiState.drawDeckGauge),
		ui.NewRow(0.8,
			ui.NewCol(0.3, uiState.tableCell),
			ui.NewCol(0.3, uiState.handCountChart),
			ui.NewCol(0.4, uiState.eventLogCell)),
		ui.NewRow(0.1,
			ui.NewCol(1.0, uiState.commandPromptCell)),
	)

	uiState.logWindowChan = logWindowChan

	uiState.debugFlags = debugFlags
}

// Runs in own thread
func (uiState *UIState) RunPollInputEvents(defaultCommandReceiverChan chan<- uknow.Command) {
	defer func() {
		uknow.Logger.Printf("Event loop exits\n")
	}()

	uiEvents := ui.PollEvents()

	curCommandReceiverChan := defaultCommandReceiverChan

	for {
		select {
		case e := <-uiEvents:
			// uiState.appendEventLog(fmt.Sprintf("Event: %+v", e))

			switch e.ID {
			case "<C-c>":
				curCommandReceiverChan <- uknow.NewCommand(uknow.CmdQuit)
				uiState.notifyRedrawUI(uiStop, func() {})
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				uiState.notifyRedrawUI(uiRedraw, func() {
					uiState.grid.SetRect(0, 0, payload.Width, payload.Height)
				})
			case "<Enter>":
				uiState.handleCommandInput(curCommandReceiverChan)
				curCommandReceiverChan = defaultCommandReceiverChan
			case "<Space>":
				uiState.appendCommandPrompt(" ")
			case "<Backspace>":
				uiState.backspaceCommandPrompt()
			case "<Up>":
				uiState.commandPromptMutex.Lock()
				if uiState.commandHistoryIndex >= 0 {
					uiState.commandStringBeingTyped = uiState.commandHistory[uiState.commandHistoryIndex]
					uiState.resetCommandPrompt(uiState.commandStringBeingTyped, false)
					uiState.commandHistoryIndex--
				}
				uiState.commandPromptMutex.Unlock()
			case "<Down>":
				uiState.commandPromptMutex.Lock()
				if uiState.commandHistoryIndex >= 0 && uiState.commandHistoryIndex < len(uiState.commandHistory)-1 {
					uiState.commandStringBeingTyped = uiState.commandHistory[uiState.commandHistoryIndex]
					uiState.resetCommandPrompt(uiState.commandStringBeingTyped, false)
				}
				if uiState.commandHistoryIndex < len(uiState.commandHistory)-1 {
					uiState.commandHistoryIndex++
				}
				uiState.commandPromptMutex.Unlock()
			default:
				// uknow.Logger.Printf("Event: %v\n", e)
				uiState.appendCommandPrompt(e.ID)
			}

		case askUIForUserTurnArgs := <-uiState.askUIForUserTurnChan:
			if askUIForUserTurnArgs.appQuitting {
				uiState.notifyRedrawUI(uiStop, func() {})
				askUIForUserTurnArgs.receive <- uknow.NewCommand(uknow.CmdQuit)
				return
			}
			uiState.allowDecisionCommands = true
			curCommandReceiverChan = askUIForUserTurnArgs.receive

		case logMessage := <-uiState.logWindowChan:
			uiState.appendEventLog(logMessage)
		}
	}
}

// Runs in own thread.
func (uiState *UIState) RunDrawLoop() {
	ui.Render(uiState.grid)
	for i := 0; ; i++ {
		uiState.actionCond.L.Lock()
		for uiState.action == uiDrawn {
			uiState.actionCond.Wait()
		}

		if uiState.action == uiStop {
			uiState.actionCond.L.Unlock()
			return
		}

		switch uiState.action {
		case uiStop:
			uiState.actionCond.L.Unlock()
			return
		case uiClearRedraw:
			ui.Clear()
			ui.Render(uiState.grid)
			uiState.action = uiDrawn
		case uiRedraw:
			uknow.Logger.Printf("Redrawing UI")
			ui.Render(uiState.grid)
			uiState.action = uiDrawn
		default:
			uknow.Logger.Fatalf("Invalid action value\n")
		}
		uiState.actionCond.L.Unlock()
	}
}

type DummyUIState struct {
	askUIForUserTurnChan <-chan askUIForUserTurnArgs
	logWindowChan        <-chan string
}

func (dummy *DummyUIState) Init(askUIForUserTurnChan <-chan askUIForUserTurnArgs, logWindowChan <-chan string) {
	dummy.askUIForUserTurnChan = askUIForUserTurnChan
	dummy.logWindowChan = logWindowChan
}

func (dummy *DummyUIState) RunAskUIDumper() {
	for args := range dummy.askUIForUserTurnChan {
		if args.appQuitting {
			args.receive <- uknow.NewCommand(uknow.CmdQuit)
			return
		}
	}
}

func (dummy *DummyUIState) RunWindowLogger() {
	for range dummy.logWindowChan {
	}
}
