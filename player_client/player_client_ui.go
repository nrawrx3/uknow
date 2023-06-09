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
	"github.com/nrawrx3/uknow"
)

//go:generate stringer -type=UIAction
type UIAction int

const (
	uiUpdated UIAction = iota
	uiRedrawGrid
	uiClearRedrawGrid
	uiStop
)

type ClientUIState string

const (
	ClientUIOnlyAllowInspectReplCommands    ClientUIState = "only_allow_inspect_repl_commands"
	ClientUIAllowPlayerDecisionReplCommands ClientUIState = "allow_player_decision_repl_commands"
	ClientUIWeHaveAWinner                   ClientUIState = "we_have_a_winner"
)

const maxLinesInEventLog = 50

type ClientUIChannels struct {
	GeneralUICommandPullChan   <-chan UICommand                    // For one-off commands and events
	AskUserForDecisionPullChan <-chan *UICommandAskUserForDecision // Tells the UI component that it should expect decision commands from player
	GameEventPullChan          <-chan uknow.GameEvent              // Consumes game events
	// Used to print logs into the event log
	LogWindowPullChan <-chan string

	GeneralReplCommandPushChan chan<- *ReplCommand
}

const numCardsToShowInPile = 12

const defaultCommandPromptCellTitle = "Not your turn (only info commands allowed)"

type ClientUI struct {
	// stateMutex protects the uiState field. We must take care to always
	// lock the mutexes in the order as they appear in this struct to
	// prevent deadlocks. So if you need to access multiple parts of this
	// struct, make sure the code path locks these in the order the mutexes
	// appear here.
	stateMutex sync.Mutex
	uiState    ClientUIState

	// Signalling the UI process that we have updated UI data is done by the actionCond and concurrent
	// rw is protected by the uiActionMutex
	uiActionMutex     sync.Mutex // protects access to every widget object
	uiActionCond      *sync.Cond // used to signal to UI goro that widget data has been updated and should be drawn
	action            UIAction
	windowWidth       int
	windowHeight      int
	grid              *ui.Grid
	pileList          *widgets.List
	commandPromptCell *widgets.Paragraph
	drawDeckGauge     *widgets.Gauge
	handCountChart    *widgets.BarChart
	selfHandWidget    *widgets.Paragraph
	discardPile       uknow.Deck    // Not a widget itself, but the pileCell gets its data from here
	playerHand        uknow.Deck    // Not widget itself, but the playerHandCell gets its data from here
	discardPileCells  []interface{} // Stores *widgets.Paragraph(s)
	eventLogCell      *widgets.Paragraph
	eventLogLines     []string

	commandPromptMutex      sync.Mutex
	commandStringBeingTyped string
	commandHistory          *HistoryRing
	commandHistoryPos       HistoryPos

	ClientUIChannels

	// Used for internal comm when the we receive a UICommandAskUserForDecision ui command
	decisionReplCommandConsumerChan chan *ReplCommand

	Logger *log.Logger
}

func (clientUI *ClientUI) notifyRedrawUI(action UIAction, exec func(), debugMessage ...string) {
	clientUI.Logger.Printf("notifyRedrawUI: %s, %v", action, debugMessage)
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
		clientUI.Logger.Printf("Before sending command to clientUI.decisionReplCommandConsumerChan")
		defer clientUI.Logger.Printf("Done sending command to clientUI.decisionReplCommandConsumerChan")
		clientUI.decisionReplCommandConsumerChan <- command
	} else {
		// clientUI.Logger.Printf("Before sending general command to clientUI.GeneralReplCommandPushChan")
		clientUI.GeneralReplCommandPushChan <- command

		// clientUI.Logger.Printf("Done sending general command to clientUI.GeneralReplCommandPushChan")
	}
	clientUI.commandHistoryPos.Push(clientUI.commandHistory, clientUI.commandStringBeingTyped)
	clientUI.resetCommandPrompt("")
}

func (clientUI *ClientUI) appendEventLog(s string) {
	clientUI.notifyRedrawUI(uiRedrawGrid, func() {
		clientUI.appendEventLogNoLock(s)
	})
}

func (clientUI *ClientUI) appendEventLogNoLock(s string) {
	clientUI.eventLogLines = append(clientUI.eventLogLines, strings.Split(s, "\n")...)

	var sb strings.Builder

	low := len(clientUI.eventLogLines) - maxLinesInEventLog
	if low < 0 {
		low = 0
	}

	for i := len(clientUI.eventLogLines) - 1; i >= low; i-- {
		sb.WriteString(clientUI.eventLogLines[i])
		sb.WriteRune('\n')
	}

	clientUI.eventLogCell.Text = sb.String()

	l := len(clientUI.eventLogLines)
	if l > maxLinesInEventLog {
		clientUI.eventLogLines = clientUI.eventLogLines[l-maxLinesInEventLog : l]
	}
}

func (clientUI *ClientUI) appendCommandPrompt(s string) {
	clientUI.commandPromptMutex.Lock()
	defer clientUI.commandPromptMutex.Unlock()
	clientUI.commandStringBeingTyped += s
	clientUI.notifyRedrawUI(uiRedrawGrid, func() {
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

	clientUI.notifyRedrawUI(uiRedrawGrid, func() {
		clientUI.commandPromptCell.Text = fmt.Sprintf(" %s_", clientUI.commandStringBeingTyped)
	})
}

func (clientUI *ClientUI) resetCommandPrompt(text string) {
	clientUI.commandStringBeingTyped = text
	clientUI.notifyRedrawUI(uiRedrawGrid, func() {
		clientUI.commandPromptCell.Text = fmt.Sprintf(" %s_", text)
	})
}

// DOES NOT LOCK actionMutex. We should perform deep copies of the table
// elements here, don't want to share deck (which are slices) between the
// clientUI and the given table. Hence we use the Clone() method while copying
// the decks.
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
	clientUI.initDiscardPileCells(table)

	// Initialize the player hand widget.
	clientUI.playerHand = table.HandOfPlayer[localPlayerName].Clone()
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
		// sb.WriteString(fmt.Sprintf("(%s|%s) ", card.Color.String(), card.Number.String()))
		sb.WriteString(card.SymbolString())
		sb.WriteString(" ")
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
	clientUI.handCountChart.MaxVal = 20

	clientUI.drawDeckGauge = widgets.NewGauge()
	clientUI.drawDeckGauge.Percent = 100
	clientUI.drawDeckGauge.BarColor = ui.ColorWhite
	// clientUI.drawDeckGauge.Title = "DrawDeck"
	clientUI.drawDeckGauge.Border = false

	clientUI.eventLogCell = widgets.NewParagraph()
	clientUI.eventLogCell.Title = "Event Log"
	clientUI.eventLogLines = make([]string, 0, 5120)

	clientUI.commandPromptCell = widgets.NewParagraph()
	clientUI.commandPromptCell.Title = defaultCommandPromptCellTitle
	clientUI.commandPromptCell.Block.BorderStyle.Fg = ui.ColorRed
	clientUI.resetCommandPrompt("")

	clientUI.commandHistory = NewHistoryRing(4)

	clientUI.selfHandWidget = widgets.NewParagraph()
	clientUI.selfHandWidget.Title = "Hand"

	clientUI.discardPile = uknow.NewEmptyDeck()
	clientUI.playerHand = uknow.NewEmptyDeck()

	clientUI.discardPileCells = make([]interface{}, 0, numCardsToShowInPile)
	for i := 0; i < numCardsToShowInPile; i++ {
		// TODO: Perhaps we can use widgets.List instead of all this
		// manual management with widgets.Paragraph and ui.Rows??
		p := widgets.NewParagraph()
		p.Text = "_"
		p.Title = ""
		clientUI.discardPileCells = append(clientUI.discardPileCells, p)
	}
}

func uiColorOfCard(color uknow.Color) ui.Color {
	switch color {
	case uknow.ColorBlue:
		return ui.ColorBlue
	case uknow.ColorRed:
		return ui.ColorRed
	case uknow.ColorGreen:
		return ui.ColorGreen
	case uknow.ColorYellow:
		return ui.ColorYellow
	case uknow.ColorWild:
		return ui.ColorMagenta
	}
	panic(fmt.Sprintf("Unexpected Color value: %d", color))
}

func (clientUI *ClientUI) initDiscardPileCells(table *uknow.Table) {
	clientUI.discardPile = table.DiscardedPile.Clone()
	clientUI.refreshDiscardPileCells()
}

func (clientUI *ClientUI) refreshDiscardPileCells() {
	low := len(clientUI.discardPile) - numCardsToShowInPile
	if low < 0 {
		low = 0
	}

	cardsToShow := clientUI.discardPile[low:len(clientUI.discardPile)]

	for i, card := range cardsToShow {
		// discardPileCells is organized high-to-low
		cellIndex := len(clientUI.discardPileCells) - i - 1
		p := clientUI.discardPileCells[cellIndex].(*widgets.Paragraph)
		// p.Text = card.String()
		p.Text = card.SymbolString()
		p.Title = fmt.Sprintf("%d", i)
		// p.TextStyle.Bg = uiColorOfCard(card.Color)
	}
}

func (clientUI *ClientUI) Init(logger *log.Logger,
	generalUICommandChan <-chan UICommand,
	askUserForDecisionChan <-chan *UICommandAskUserForDecision,
	generalReplCommandPushChan chan<- *ReplCommand,
	gameEventPullChan <-chan uknow.GameEvent,
	logWindowChan <-chan string) {
	if err := ui.Init(); err != nil {
		log.Fatalf("Failed to initialized termui: %v", err)
	}
	clientUI.Logger = logger
	clientUI.uiState = ClientUIOnlyAllowInspectReplCommands

	clientUI.uiActionCond = sync.NewCond(&clientUI.uiActionMutex)
	clientUI.action = uiRedrawGrid

	clientUI.initWidgetObjects()

	clientUI.GeneralUICommandPullChan = generalUICommandChan
	clientUI.AskUserForDecisionPullChan = askUserForDecisionChan
	clientUI.GameEventPullChan = gameEventPullChan
	clientUI.GeneralReplCommandPushChan = generalReplCommandPushChan

	clientUI.grid = ui.NewGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	clientUI.grid.SetRect(0, 0, termWidth, termHeight)

	clientUI.embedWidgetsInGrid()

	clientUI.LogWindowPullChan = logWindowChan

	clientUI.decisionReplCommandConsumerChan = make(chan *ReplCommand)
}

func (clientUI *ClientUI) embedWidgetsInGrid() {
	pileCellRows := make([]interface{}, 0, numCardsToShowInPile)
	sizePerPileCell := 1.0 / numCardsToShowInPile
	for _, pileCell := range clientUI.discardPileCells {
		pileCellRows = append(pileCellRows, ui.NewRow(sizePerPileCell, pileCell))
	}

	clientUI.grid.Set(
		ui.NewRow(0.02, clientUI.drawDeckGauge),
		ui.NewRow(0.8,
			ui.NewCol(0.3, pileCellRows...),
			ui.NewCol(0.3, clientUI.handCountChart),
			ui.NewCol(0.4, clientUI.eventLogCell)),
		ui.NewRow(0.08, clientUI.selfHandWidget),
		ui.NewRow(0.1, clientUI.commandPromptCell),
	)
}

func (clientUI *ClientUI) RunGeneralUICommandConsumer(localPlayerName string) {
	for uiCommand := range clientUI.GeneralUICommandPullChan {
		switch cmd := uiCommand.(type) {
		case *UICommandSetServedCards:
			clientUI.appendEventLog("Received UICommandSetServedCards")

			clientUI.notifyRedrawUI(uiRedrawGrid, func() {
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
		clientUI.Logger.Printf("Event loop exits\n")
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
				clientUI.notifyRedrawUI(uiRedrawGrid, func() {
					clientUI.windowWidth = payload.Width
					clientUI.windowHeight = payload.Height
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
				prevCommand, ok := clientUI.commandHistoryPos.GetOlder(clientUI.commandHistory)
				if ok {
					clientUI.resetCommandPrompt(prevCommand)
				}
				clientUI.commandPromptMutex.Unlock()
			case "<Down>":
				clientUI.commandPromptMutex.Lock()
				nextCommand, ok := clientUI.commandHistoryPos.GetNewer(clientUI.commandHistory)
				if ok {
					clientUI.resetCommandPrompt(nextCommand)
				}
				clientUI.commandPromptMutex.Unlock()
			default:
				if !strings.HasPrefix(e.ID, "<") && !strings.HasSuffix(e.ID, ">") {
					clientUI.appendCommandPrompt(e.ID)
				}
				// clientUI.Logger.Printf("Event: %v\n", e)
			}

		case askUserForDecisionCommand := <-clientUI.AskUserForDecisionPullChan:
			clientUI.stateMutex.Lock()

			if clientUI.uiState == ClientUIWeHaveAWinner {
				clientUI.stateMutex.Unlock()
				clientUI.appendEventLog("Not processing user decision since we have a winner")
				continue
			}

			clientUI.uiState = ClientUIAllowPlayerDecisionReplCommands
			clientUI.stateMutex.Unlock()

			// Change the UI style a bit to make it obvious it's the local player's turn
			clientUI.notifyRedrawUI(uiRedrawGrid, func() {
				clientUI.commandPromptCell.Block.BorderStyle.Fg = ui.ColorBlue
				clientUI.commandPromptCell.TextStyle.Fg = ui.ColorBlue
				// clientUI.drawDeckGauge.BarColor = ui.ColorBlue
				clientUI.commandPromptCell.Title = "Your turn now"
			})

			go func() {
				if askUserForDecisionCommand.LocalPlayerCanChallenge() {
					clientUI.notifyRedrawUI(uiRedrawGrid, func() {
						clientUI.commandPromptCell.Title = fmt.Sprintf("challenge or no_challenge %s?", askUserForDecisionCommand.challengeablePlayer)
					})

					defer func() {
						clientUI.commandPromptCell.Title = defaultCommandPromptCellTitle
					}()
				}

				for decisionReplCommand := range clientUI.decisionReplCommandConsumerChan {
					// Convert to PlayerDecisionEvent
					askUserForDecisionCommand.receive <- decisionReplCommand
					decisionResult := <-askUserForDecisionCommand.decisionResultChan

					if decisionResult.Error != nil {
						// CONSIDER(@rk): Does it make sense to show a bit more fancy signal in case he makes an illegal decision?

						go func() {
							clientUI.notifyRedrawUI(uiRedrawGrid, func() {
								// clientUI.drawDeckGauge.BarColor = ui.ColorRed
								clientUI.eventLogCell.BorderStyle.Fg = ui.ColorRed

							})
							<-time.After(2 * time.Second)
							clientUI.notifyRedrawUI(uiRedrawGrid, func() {
								// clientUI.drawDeckGauge.BarColor = ui.ColorBlue
								clientUI.eventLogCell.BorderStyle.Fg = ui.ColorWhite
							})
						}()
						clientUI.appendEventLog(decisionResult.Error.Error())
					}

					if !decisionResult.AskForOneMoreDecision {
						break
					}

					clientUI.Logger.Printf("need more decision from user")
				}

				close(askUserForDecisionCommand.receive)

				clientUI.stateMutex.Lock()
				if clientUI.uiState != ClientUIWeHaveAWinner {
					clientUI.uiState = ClientUIOnlyAllowInspectReplCommands
					clientUI.appendEventLog("Done accepting decision commands in REPL")

					// Reset the UI style as the local player's turn is over
					clientUI.notifyRedrawUI(uiRedrawGrid, func() {
						clientUI.commandPromptCell.Block.BorderStyle.Fg = ui.ColorRed
						clientUI.commandPromptCell.TextStyle.Fg = ui.ColorWhite
						// clientUI.drawDeckGauge.BarColor = ui.ColorWhite
						clientUI.commandPromptCell.Title = defaultCommandPromptCellTitle
					})
				}
				clientUI.stateMutex.Unlock()
			}()

		case logMessage := <-clientUI.LogWindowPullChan:
			clientUI.appendEventLog(logMessage)
		}
	}
}

func (clientUI *ClientUI) RunGameEventProcessor(localPlayerName string) {
	for event := range clientUI.GameEventPullChan {
		switch event := event.(type) {
		case uknow.RequiredColorUpdatedEvent:
			clientUI.notifyRedrawUI(uiRedrawGrid, func() {
				clientUI.drawDeckGauge.BarColor = uiColorOfCard(event.NewColor)
			})

		case uknow.CardTransferEvent:
			// clientUI.Logger.Printf("received")

			clientUI.notifyRedrawUI(uiRedrawGrid, func() {
				clientUI.handleCardTransferEvent(event, localPlayerName)
			})

		case uknow.AwaitingWildCardColorDecisionEvent:
			if event.AskDecisionFromLocalPlayer {
				clientUI.appendEventLog(event.StringMessage(localPlayerName))
			}

		case uknow.ChallengerSuccessEvent:
			if event.FromLocalClient() { // Set challenge result info as command prompt
				clientUI.notifyRedrawUI(uiRedrawGrid, func() {
					clientUI.commandPromptCell.Title = fmt.Sprintf("CHALLENGE SUCCEEDED, %s will draw 4 cards", event.WildDraw4PlayerName)
				})
			}

		case uknow.ChallengerFailedEvent:
			if event.FromLocalClient() {
				// Set challenge result info as command prompt
				clientUI.notifyRedrawUI(uiRedrawGrid, func() {
					clientUI.commandPromptCell.Title = "CHALLENGE FAILED, you will draw 6 cards instead of 4"
				})

				// Reset after a bit
				go clientUI.runResetCommandPromptTitle(3 * time.Second)
			}

		case uknow.AwaitingPlayOrPassEvent:
			if event.FromLocalClient() {
				clientUI.notifyRedrawUI(uiRedrawGrid, func() {
					clientUI.commandPromptCell.Title = "Please play a card or pass"
				})
			}

		case uknow.PlayerPassedTurnEvent:
			if event.IsFromLocalClient {
				clientUI.stateMutex.Lock()

				if clientUI.uiState != ClientUIWeHaveAWinner {
					clientUI.notifyRedrawUI(uiRedrawGrid, func() { clientUI.commandPromptCell.Title = defaultCommandPromptCellTitle })
				}

				clientUI.stateMutex.Unlock()
			}

		case uknow.PlayerHasWonEvent:
			clientUI.stateMutex.Lock()
			clientUI.uiState = ClientUIWeHaveAWinner
			clientUI.notifyRedrawUI(uiRedrawGrid, func() {
				clientUI.commandPromptCell.Title = event.StringMessage(localPlayerName)
			}, event.Player, "won the game")
			clientUI.stateMutex.Unlock()

		default:
			clientUI.Logger.Printf("UNKNOWN GAME EVENT: %s", event.GameEventName())
			clientUI.appendEventLog("<Unknown game event received>")
		}
	}
}

func (clientUI *ClientUI) runResetCommandPromptTitle(timeout time.Duration) {
	<-time.After(timeout)

	clientUI.stateMutex.Lock()
	defer clientUI.stateMutex.Unlock()

	clientUI.notifyRedrawUI(uiRedrawGrid, func() {
		clientUI.setTurnBasedCommandPromptTitle(defaultCommandPromptCellTitle)
	})
}

// Does not lock stateMutex
func (clientUI *ClientUI) setTurnBasedCommandPromptTitle(title string) {
	if clientUI.uiState != ClientUIWeHaveAWinner {
		clientUI.commandPromptCell.Title = title
	}
}

func (clientUI *ClientUI) handleCardTransferEvent(event uknow.CardTransferEvent, localPlayerName string) {
	// TODO(@rk): Don't show the card info if the card transfer is happening
	// to hand of non local player
	clientUI.appendEventLogNoLock(fmt.Sprintf("handleCardTransferEvent: %s, localPlayerName: %s", event.String(localPlayerName), localPlayerName))

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
			clientUI.updatePlayerHandWidget()
		}
	}

	<-time.After(500 * time.Millisecond)

	switch event.Sink {
	case uknow.CardTransferNodeDeck:
		clientUI.drawDeckGauge.Percent += 1
	case uknow.CardTransferNodePile:
		clientUI.discardPile = clientUI.discardPile.Push(event.Card)
		clientUI.refreshDiscardPileCells()
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
		for clientUI.action == uiUpdated {
			clientUI.uiActionCond.Wait()
		}

		clientUI.Logger.Printf("Received UI action: %s", clientUI.action)

		switch clientUI.action {
		case uiStop:
			clientUI.uiActionCond.L.Unlock()
			return
		case uiClearRedrawGrid:
			ui.Clear()
			ui.Render(clientUI.grid)
			clientUI.action = uiUpdated
		case uiRedrawGrid:
			// clientUI.Logger.Printf("Redrawing UI")
			ui.Render(clientUI.grid)
			clientUI.action = uiUpdated
		default:
			clientUI.Logger.Fatalf("Invalid action value\n")
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
