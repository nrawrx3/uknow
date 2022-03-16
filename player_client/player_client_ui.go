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

type ClientUIState string

const (
	ClientUIOnlyAllowInspectReplCommands    ClientUIState = "only_allow_inspect_repl_commands"
	ClientUIAllowPlayerDecisionReplCommands ClientUIState = "allow_player_decision_repl_commands"
)

type ClientUIChannels struct {
	GeneralUICommandPullChan   <-chan UICommand
	AskUserForDecisionPullChan <-chan *UICommandAskUserForDecision

	GeneralReplCommandPushChan chan<- *ReplCommand

	// Used to print logs into the event log
	LogWindowPullChan <-chan string
}

type ClientUI struct {
	// stateMutex protects the uiState field. We must take care to always lock the mutexes in the order as they appear in this struct to prevent deadlocks.
	// So if you need to access multiple parts of this struct, make sure the code path locks these in the order the mutexes appear in the struct.
	stateMutex sync.Mutex
	uiState    ClientUIState

	// Signalling the UI process that we have updated UI data is done by the actionCond and concurrent
	// rw is protected by the actionMutex
	actionMutex       sync.Mutex // protects access to every widget object
	actionCond        *sync.Cond // used to signal to UI goro that widget data has been updated and should be drawn
	action            uiAction
	grid              *ui.Grid
	pileCell          *widgets.Paragraph
	eventLogCell      *widgets.Paragraph
	commandPromptCell *widgets.Paragraph
	drawDeckGauge     *widgets.Gauge
	handCountChart    *widgets.BarChart

	commandPromptMutex      sync.Mutex
	commandStringBeingTyped string
	commandHistory          []string
	commandHistoryIndex     int

	ClientUIChannels

	// Used for internal comm when the we receive a UICommandAskUserForDecision ui command
	decisionReplCommandConsumerChan chan *ReplCommand

	debugFlags DebugFlags
}

func (clientUI *ClientUI) notifyRedrawUI(action uiAction, exec func()) {
	clientUI.actionCond.L.Lock()
	defer clientUI.actionCond.L.Unlock()
	exec()
	clientUI.action = action
	clientUI.actionCond.Signal()
}

func (clientUI *ClientUI) handleCommandInput(playerName string) {
	clientUI.stateMutex.Lock()
	defer clientUI.stateMutex.Unlock()

	clientUI.commandPromptMutex.Lock()
	defer clientUI.commandPromptMutex.Unlock()

	command, err := ParseCommandFromInput(clientUI.commandStringBeingTyped, playerName)

	if err != nil {
		clientUI.appendEventLog(err.Error())
		return
	}

	clientUI.appendEventLog(fmt.Sprintf("ClientUI received command: %s", command.Kind.String()))

	if command.Kind.IsUserDecisionCommand() {
		if clientUI.uiState != ClientUIAllowPlayerDecisionReplCommands {
			clientUI.appendEventLog("User decision commands not allowed currently!")
			return
		}
		uknow.Logger.Printf("Before sending command to clientUI.decisionReplCommandConsumerChan")
		defer uknow.Logger.Printf("Done sending command to clientUI.decisionReplCommandConsumerChan")
		clientUI.decisionReplCommandConsumerChan <- command
	} else {
		uknow.Logger.Printf("Before sending general command to clientUI.GeneralReplCommandPushChan")
		clientUI.GeneralReplCommandPushChan <- command

		uknow.Logger.Printf("Done sending general command to clientUI.GeneralReplCommandPushChan")
	}

	clientUI.resetCommandPrompt("", true)
}

func (clientUI *ClientUI) logUserDecisionCommand(command ReplCommand, commandChan chan<- ReplCommand) {
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
	clientUI.pileCell = widgets.NewParagraph()
	clientUI.pileCell.Title = "Table"

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
	askUserForDecisionChan <-chan *UICommandAskUserForDecision,
	generalReplCommandPushChan chan<- *ReplCommand,
	logWindowChan <-chan string) {
	if err := ui.Init(); err != nil {
		log.Fatalf("Failed to initialized termui: %v", err)
	}
	clientUI.uiState = ClientUIOnlyAllowInspectReplCommands

	clientUI.actionCond = sync.NewCond(&clientUI.actionMutex)
	clientUI.action = uiRedraw

	clientUI.initWidgetObjects()

	clientUI.GeneralUICommandPullChan = generalUICommandChan
	clientUI.AskUserForDecisionPullChan = askUserForDecisionChan
	clientUI.GeneralReplCommandPushChan = generalReplCommandPushChan

	clientUI.grid = ui.NewGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	clientUI.grid.SetRect(0, 0, termWidth, termHeight)

	clientUI.grid.Set(
		ui.NewRow(0.05, clientUI.drawDeckGauge),
		ui.NewRow(0.8,
			ui.NewCol(0.3, clientUI.pileCell),
			ui.NewCol(0.3, clientUI.handCountChart),
			ui.NewCol(0.4, clientUI.eventLogCell)),
		ui.NewRow(0.1,
			ui.NewCol(1.0, clientUI.commandPromptCell)),
	)

	clientUI.LogWindowPullChan = logWindowChan

	clientUI.decisionReplCommandConsumerChan = make(chan *ReplCommand)

	clientUI.debugFlags = debugFlags
}

func (clientUI *ClientUI) RunGeneralUICommandConsumer() {
	for uiCommand := range clientUI.GeneralUICommandPullChan {
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
func (clientUI *ClientUI) RunPollInputEvents(playerName string) {
	defer func() {
		uknow.Logger.Printf("Event loop exits\n")
	}()

	uiEvents := ui.PollEvents()

	for {
		select {
		case e := <-uiEvents:
			// clientUI.appendEventLog(fmt.Sprintf("Event: %+v", e))

			switch e.ID {
			case "<C-c>":
				clientUI.GeneralReplCommandPushChan <- NewReplCommand(CmdQuit, playerName)
				clientUI.notifyRedrawUI(uiStop, func() {})
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				clientUI.notifyRedrawUI(uiRedraw, func() {
					clientUI.grid.SetRect(0, 0, payload.Width, payload.Height)
				})
			case "<Enter>":
				clientUI.handleCommandInput(playerName)
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

		case askUserForDecisionCommand := <-clientUI.AskUserForDecisionPullChan:
			// TODO(@rk): We should show some kind of signal in UI denoting that "it's now the local players turn"
			clientUI.appendEventLog("It's your turn! Time to make decision")

			clientUI.stateMutex.Lock()
			clientUI.uiState = ClientUIAllowPlayerDecisionReplCommands
			clientUI.stateMutex.Unlock()

			go func() {
				for decisionReplCommand := range clientUI.decisionReplCommandConsumerChan {
					// Convert to PlayerDecisionEvent
					askUserForDecisionCommand.receive <- decisionReplCommand
					allowOneMoreDecision := <-askUserForDecisionCommand.allowOneMoreDecision

					if !allowOneMoreDecision {
						break
					}
				}

				close(askUserForDecisionCommand.receive)

				// TODO(@rk): Without any proper game-logic, this reverting to "only-inspect-command" is being hit after just 1 repl command input.
				// We need to be communicated by the PlayerClient if we can continue the above loop, i.e. allow more decision commands or not.
				clientUI.stateMutex.Lock()
				clientUI.uiState = ClientUIOnlyAllowInspectReplCommands
				clientUI.stateMutex.Unlock()

				clientUI.appendEventLog("Done accepting decision commands in REPL")
			}()

		case logMessage := <-clientUI.LogWindowPullChan:
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

func (dummy *DummyClientUI) Init(generalUICommandChan <-chan UICommand, askUIForUserInput <-chan *UICommandAskUserForDecision, logWindowChan <-chan string) {
	dummy.GeneralUICommandPullChan = generalUICommandChan
	dummy.AskUserForDecisionPullChan = askUIForUserInput
	dummy.LogWindowPullChan = logWindowChan
}

func (dummy *DummyClientUI) RunAskUIDumper() {
	for range dummy.AskUserForDecisionPullChan {
	}
}

func (dummy *DummyClientUI) RunWindowLogger() {
	for range dummy.LogWindowPullChan {
	}
}
