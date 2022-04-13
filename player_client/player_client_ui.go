package client

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

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
	GeneralUICommandPullChan   <-chan UICommand                    // For one-off commands and events
	AskUserForDecisionPullChan <-chan *UICommandAskUserForDecision // Tells the UI component that it should expect decision commands from player
	CardTransferEventPullChan  <-chan uknow.CardTransferEvent      // Consumes card transfer events
	// Used to print logs into the event log
	LogWindowPullChan <-chan string

	GeneralReplCommandPushChan chan<- *ReplCommand
}

const numCardsToShowInPile = 10

type ClientUI struct {
	// stateMutex protects the uiState field. We must take care to always lock the mutexes in the order as they appear in this struct to prevent deadlocks.
	// So if you need to access multiple parts of this struct, make sure the code path locks these in the order the mutexes appear in the struct.
	stateMutex sync.Mutex
	uiState    ClientUIState

	// Signalling the UI process that we have updated UI data is done by the actionCond and concurrent
	// rw is protected by the uiActionMutex
	uiActionMutex     sync.Mutex // protects access to every widget object
	uiActionCond      *sync.Cond // used to signal to UI goro that widget data has been updated and should be drawn
	action            uiAction
	grid              *ui.Grid
	pileList          *widgets.List
	eventLogCell      *widgets.Paragraph
	commandPromptCell *widgets.Paragraph
	drawDeckGauge     *widgets.Gauge
	handCountChart    *widgets.BarChart
	selfHandWidget    *widgets.Paragraph
	discardPile       uknow.Deck    // Not a widget itself, but the pileCell gets its data from here
	playerHand        uknow.Deck    // Not widget itself, but the playerHandCell gets its data from here
	discardPileCells  []interface{} // Stores *widgets.Paragraph(s)

	commandPromptMutex      sync.Mutex
	commandStringBeingTyped string
	commandHistory          []string // TODO(@rk): Fix this. =)
	commandHistoryIndex     int

	ClientUIChannels

	// Used for internal comm when the we receive a UICommandAskUserForDecision ui command
	decisionReplCommandConsumerChan chan *ReplCommand

	debugFlags DebugFlags
}

func (clientUI *ClientUI) notifyRedrawUI(action uiAction, exec func()) {
	clientUI.uiActionCond.L.Lock()
	defer clientUI.uiActionCond.L.Unlock()
	exec()
	clientUI.action = action
	clientUI.uiActionCond.Signal()
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

func (clientUI *ClientUI) appendEventLog(line string) {
	clientUI.notifyRedrawUI(uiRedraw, func() {
		clientUI.eventLogCell.Text = fmt.Sprintf("%s\n%s", clientUI.eventLogCell.Text, line)
		// uknow.Logger.Println(line)
	})
}

func (clientUI *ClientUI) appendEventLogNoLock(line string) {
	clientUI.eventLogCell.Text = fmt.Sprintf("%s\n%s", clientUI.eventLogCell.Text, line)
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
func (clientUI *ClientUI) initTableElements(table *uknow.Table, localPlayerName string) {
	// Initialize the handCountChart
	playerCount := len(table.PlayerNames)

	if len(clientUI.handCountChart.Labels) < playerCount {
		clientUI.handCountChart.Labels = make([]string, playerCount)
		clientUI.handCountChart.Data = make([]float64, playerCount)
	} else {
		clientUI.handCountChart.Labels = clientUI.handCountChart.Labels[0:playerCount]
		clientUI.handCountChart.Data = clientUI.handCountChart.Data[0:playerCount]
	}

	clientUI.sortHandCountChartByTurn(table)

	// Initialize the draw deck
	clientUI.drawDeckGauge.Percent = table.DrawDeck.Len()

	// Update the pile cells
	clientUI.updateDiscardPileCells()

	// Initialize the player hand widget
	for _, card := range table.HandOfPlayer[localPlayerName] {
		clientUI.playerHand = clientUI.playerHand.Push(card)
	}
	sort.Sort(clientUI.playerHand)
	clientUI.updatePlayerHandWidget()
}

func (clientUI *ClientUI) sortHandCountChartByTurn(table *uknow.Table) {
	chart := clientUI.handCountChart

	for i, playerIndex := range table.PlayerIndicesSortedByTurn() {
		playerName := table.PlayerNames[playerIndex]
		chart.Labels[i] = playerName
		chart.Data[i] = float64(len(table.HandOfPlayer[playerName]))
	}

	for i := range chart.Labels {
		if i == 0 {
			chart.LabelStyles[i] = ui.NewStyle(ui.ColorRed)
		} else {
			chart.LabelStyles[i] = ui.NewStyle(ui.ColorBlue)
		}
	}

	clientUI.appendEventLogNoLock(fmt.Sprintf("Handcount chart labels set to: %v", clientUI.handCountChart.Labels))
}

// **DOES NOT LOCK** uiActionMutex
func (clientUI *ClientUI) updatePlayerHandWidget() {
	var sb strings.Builder
	for _, card := range clientUI.playerHand {
		sb.WriteString(fmt.Sprintf("(%s|%s) ", card.Color.String(), card.Number.String()))
	}
	clientUI.selfHandWidget.Text = sb.String()
}

// Creates and initializes the widget structs. All updates to the UI happens via modifying data in these
// structs. So even if we don't have a ui goro running, these structs can be modified anyway - no need to
// check first if ui is disabled or not
func (clientUI *ClientUI) initWidgetObjects() {
	clientUI.pileList = widgets.NewList()
	clientUI.pileList.Title = "Discard Pile"
	clientUI.pileList.Border = true
	clientUI.pileList.TitleStyle = ui.NewStyle(ui.ColorYellow)
	clientUI.pileList.TextStyle = ui.NewStyle(ui.ColorYellow)
	clientUI.pileList.Rows = make([]string, 0, 64)

	clientUI.handCountChart = widgets.NewBarChart()
	clientUI.handCountChart.Labels = make([]string, 0, 16)
	clientUI.handCountChart.Data = make([]float64, 0, 16)
	clientUI.handCountChart.Title = "Hand count"

	clientUI.drawDeckGauge = widgets.NewGauge()
	clientUI.drawDeckGauge.Percent = 100
	clientUI.drawDeckGauge.BarColor = ui.ColorWhite
	clientUI.drawDeckGauge.Title = "DrawDeck"
	clientUI.drawDeckGauge.Border = false

	clientUI.eventLogCell = widgets.NewParagraph()
	clientUI.eventLogCell.Title = "Event Log"

	clientUI.commandPromptCell = widgets.NewParagraph()
	clientUI.commandPromptCell.Title = "Command Input"
	clientUI.resetCommandPrompt("", false)

	clientUI.commandHistoryIndex = -1
	clientUI.commandHistory = make([]string, 0, 64)

	clientUI.selfHandWidget = widgets.NewParagraph()
	clientUI.selfHandWidget.Title = "Hand"

	clientUI.discardPile = uknow.NewEmptyDeck()
	clientUI.playerHand = uknow.NewEmptyDeck()

	clientUI.discardPileCells = make([]interface{}, 0, numCardsToShowInPile)
	for i := 0; i < numCardsToShowInPile; i++ {
		p := widgets.NewParagraph()
		p.Text = "EMPTY PILE CELL"
		p.Title = "PileCell"
		p.TextStyle.Bg = ui.ColorRed
		clientUI.discardPileCells = append(clientUI.discardPileCells, p)
	}
}

func uiColorOfCard(color uknow.Color) ui.Color {
	switch color {
	case uknow.Blue:
		return ui.ColorBlue
	case uknow.Red:
		return ui.ColorRed
	case uknow.Green:
		return ui.ColorGreen
	case uknow.Yellow:
		return ui.ColorYellow
	}
	panic(fmt.Sprintf("Unexpected Color value: %d", color))
}

func (clientUI *ClientUI) updateDiscardPileCells() {
	low := len(clientUI.discardPile) - numCardsToShowInPile
	if low < 0 {
		low = 0
	}

	cardsToShow := clientUI.discardPile[low:len(clientUI.discardPile)]

	for i, card := range cardsToShow {
		p := clientUI.discardPileCells[i].(*widgets.Paragraph)
		p.Text = card.Number.String()
		p.TextStyle.Bg = uiColorOfCard(card.Color)
	}
}

func (clientUI *ClientUI) Init(debugFlags DebugFlags,
	generalUICommandChan <-chan UICommand,
	askUserForDecisionChan <-chan *UICommandAskUserForDecision,
	generalReplCommandPushChan chan<- *ReplCommand,
	cardTransferEventPullChan <-chan uknow.CardTransferEvent,
	logWindowChan <-chan string) {
	if err := ui.Init(); err != nil {
		log.Fatalf("Failed to initialized termui: %v", err)
	}
	clientUI.uiState = ClientUIOnlyAllowInspectReplCommands

	clientUI.uiActionCond = sync.NewCond(&clientUI.uiActionMutex)
	clientUI.action = uiRedraw

	clientUI.initWidgetObjects()

	clientUI.GeneralUICommandPullChan = generalUICommandChan
	clientUI.AskUserForDecisionPullChan = askUserForDecisionChan
	clientUI.CardTransferEventPullChan = cardTransferEventPullChan
	clientUI.GeneralReplCommandPushChan = generalReplCommandPushChan

	clientUI.grid = ui.NewGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	clientUI.grid.SetRect(0, 0, termWidth, termHeight)

	pileCellRows := make([]interface{}, 0, numCardsToShowInPile)
	sizePerPileCell := 1.0 / numCardsToShowInPile
	for _, pileCell := range clientUI.discardPileCells {
		pileCellRows = append(pileCellRows, ui.NewRow(sizePerPileCell, pileCell))
	}

	clientUI.grid.Set(
		ui.NewRow(0.1, clientUI.drawDeckGauge),
		ui.NewRow(0.8,
			ui.NewCol(0.3, pileCellRows...),
			ui.NewCol(0.3, clientUI.handCountChart),
			ui.NewCol(0.4, clientUI.eventLogCell)),
		ui.NewRow(0.1, ui.NewCol(0.5, clientUI.selfHandWidget), ui.NewCol(0.5, clientUI.commandPromptCell)),
	)

	clientUI.LogWindowPullChan = logWindowChan

	clientUI.decisionReplCommandConsumerChan = make(chan *ReplCommand)

	clientUI.debugFlags = debugFlags
}

func (clientUI *ClientUI) RunGeneralUICommandConsumer(localPlayerName string) {
	for uiCommand := range clientUI.GeneralUICommandPullChan {
		switch cmd := uiCommand.(type) {
		case *UICommandSetServedCards:
			clientUI.appendEventLog("Received UICommandSetServedCards")

			clientUI.notifyRedrawUI(uiRedraw, func() {
				clientUI.drawDeckGauge.Percent = cmd.table.DrawDeck.Len()
				clientUI.initTableElements(cmd.table, localPlayerName)
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
				if !strings.HasPrefix(e.ID, "<") && !strings.HasSuffix(e.ID, ">") {
					clientUI.appendCommandPrompt(e.ID)
				}
				// uknow.Logger.Printf("Event: %v\n", e)
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

func (clientUI *ClientUI) RunCardTransferEventProcessor(localPlayerName string) {
	for event := range clientUI.CardTransferEventPullChan {
		clientUI.notifyRedrawUI(uiRedraw, func() {
			clientUI.handleCardTransferEvent(event, localPlayerName)
		})
	}
}

func (clientUI *ClientUI) handleCardTransferEvent(event uknow.CardTransferEvent, localPlayerName string) {
	clientUI.appendEventLogNoLock(fmt.Sprintf("handleCardTransferEvent: %s, playerName: %s", event.String(), localPlayerName))

	switch event.Source {
	case uknow.CardTransferNodeDeck:
		clientUI.drawDeckGauge.Percent -= 1
	case uknow.CardTransferNodePile:
		var err error
		clientUI.discardPile, err = clientUI.discardPile.Pop()
		if err != nil {
			clientUI.appendEventLogNoLock("handleCardTransferEvent failed: Transfer from empty pile")
			return
		}

	case uknow.CardTransferNodePlayerHand:
		clientUI.addToHandCountChart(event.SourcePlayer, -1)
		if localPlayerName == event.SourcePlayer {
			var err error
			clientUI.playerHand, err = clientUI.playerHand.FindAndRemoveCard(event.Card)
			if err != nil {
				clientUI.appendEventLog(fmt.Sprintf("handleCardTransferEvent failed: %s", err))
			}
		}
	}

	<-time.After(500 * time.Millisecond)

	switch event.Sink {
	case uknow.CardTransferNodeDeck:
		clientUI.drawDeckGauge.Percent += 1
	case uknow.CardTransferNodePile:
		clientUI.discardPile = clientUI.discardPile.Push(event.Card)
	case uknow.CardTransferNodePlayerHand:
		clientUI.addToHandCountChart(event.SinkPlayer, 1)
		if localPlayerName == event.SinkPlayer {
			clientUI.playerHand = clientUI.playerHand.Push(event.Card)
			sort.Sort(clientUI.playerHand)
			clientUI.updatePlayerHandWidget()
		}
	}
}

func (clientUI *ClientUI) addToHandCountChart(playerName string, cardCount int) {
	chart := clientUI.handCountChart
	for i, chartPlayerName := range chart.Labels {
		if chartPlayerName == playerName {
			chart.Data[i] += float64(cardCount)
			return
		}
	}
	clientUI.appendEventLogNoLock(fmt.Sprintf("addToHandCountChart failed: did not find playerName %s", playerName))
}

// Runs in own thread.
func (clientUI *ClientUI) RunDrawLoop() {
	ui.Render(clientUI.grid)
	for i := 0; ; i++ {
		clientUI.uiActionCond.L.Lock()
		for clientUI.action == uiDrawn {
			clientUI.uiActionCond.Wait()
		}

		if clientUI.action == uiStop {
			clientUI.uiActionCond.L.Unlock()
			return
		}

		switch clientUI.action {
		case uiStop:
			clientUI.uiActionCond.L.Unlock()
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
		clientUI.uiActionCond.L.Unlock()
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
