package main

import (
	"fmt"
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/rksht/uknow"
	"log"
	"net"
	"net/rpc"
	"time"
)

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type Console struct {
	// MUSTDO: Need a mutex to make accesses to this struct thread-safe
	grid              *ui.Grid
	tableCell         *widgets.Paragraph
	eventLogCell      *widgets.Paragraph
	commandPromptCell *widgets.Paragraph
	drawDeckGauge     *widgets.Gauge
	handCountChart    *widgets.BarChart

	commandStringBeingTyped string

	commandHistory      []string
	commandHistoryIndex int

	table *uknow.Table

	rpcClients []*rpc.Client
	rpcServer  *rpc.Server
	listener   *net.Listener

	remoteAddrOfPlayer map[string]string
}

type ConsoleLog struct {
	c *Console
}

func (clog *ConsoleLog) Write(bytes []byte) (int, error) {
	clog.c.appendEventLog(string(bytes[:]))
	return len(bytes), nil
}

// The whole UI state is in this global.
var c Console

func (c *Console) initServer() {
	c.rpcServer = rpc.NewServer()
	c.rpcServer.RegisterName("Console", &c)
}

func (c *Console) executeCommandCell() {
	command, err := uknow.ParseCommandFromInput(c.commandStringBeingTyped)

	if err != nil {
		c.appendEventLog(err.Error())
	} else {
		c.executeCommand(command)
		c.appendEventLog(fmt.Sprintf("%v", command))
		c.commandHistory = append(c.commandHistory, c.commandStringBeingTyped)
		c.commandHistoryIndex = len(c.commandHistory) - 1
		c.resetCommandPrompt("")
	}
}

func (c *Console) executeCommand(command uknow.InputCommand) {
	switch command.Kind {
	case uknow.CmdConnect:
		if c.table.State != uknow.StateBeforeConnect {
			c.appendEventLog("Game already started, cannot connect anymore")
			return
		}
		c.appendEventLog(fmt.Sprintf("Connect to remote: %s", command.ConnectAddress))
	}
}

func (c *Console) appendEventLog(line string) {
	c.eventLogCell.Text = fmt.Sprintf("%s\n%s", c.eventLogCell.Text, line)
}

func (c *Console) appendCommandPrompt(s string) {
	c.commandStringBeingTyped += s
	c.commandPromptCell.Text = fmt.Sprintf(" %s_", c.commandStringBeingTyped)
}

func (c *Console) backspaceCommandPrompt() {
	n := len(c.commandStringBeingTyped)
	if n >= 1 {
		c.commandStringBeingTyped = c.commandStringBeingTyped[0 : n-1]
	}
	c.commandPromptCell.Text = fmt.Sprintf(" %s_", c.commandStringBeingTyped)
	c.commandHistoryIndex = maxInt(0, len(c.commandHistory)-1)
}

func (c *Console) resetCommandPrompt(text string) {
	c.commandPromptCell.Text = fmt.Sprintf(" %s_", text)
	c.commandStringBeingTyped = text
}

func (c *Console) refillHandcountChart() {
	for i, playerName := range c.table.PlayerNames {
		c.handCountChart.Labels[i] = playerName
		c.handCountChart.Data[i] = float64(len(c.table.HandOfPlayer[playerName]))
	}
}

func main() {
	if err := ui.Init(); err != nil {
		log.Fatalf("failed to initialized termui: %v", err)
	}

	defer ui.Close()

	c.table = uknow.NewTable("handsome_jack")

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
	c.resetCommandPrompt("")

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

	ui.Render(c.grid)
	tickerCount := 1
	uiEvents := ui.PollEvents()
	ticker := time.NewTicker(time.Second).C

	uknow.Logger = log.New(&ConsoleLog{c: &c}, "", log.Ltime)

	for {
		select {
		case e := <-uiEvents:
			// c.appendEventLog(fmt.Sprintf("Event: %+v", e))

			switch e.ID {
			case "<C-c>":
				return
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				c.grid.SetRect(0, 0, payload.Width, payload.Height)
				ui.Clear()
				ui.Render(c.grid)

			case "<Enter>":
				c.executeCommandCell()
				ui.Render(c.grid)

			case "<Space>":
				c.appendCommandPrompt(" ")
				ui.Render(c.grid)

			case "<Backspace>":
				c.backspaceCommandPrompt()
				ui.Render(c.grid)

			case "<Up>":
				if c.commandHistoryIndex >= 0 {
					c.commandStringBeingTyped = c.commandHistory[c.commandHistoryIndex]
					c.resetCommandPrompt(c.commandStringBeingTyped)
					ui.Render(c.grid)
					c.commandHistoryIndex--
				}

			case "<Down>":
				if c.commandHistoryIndex >= 0 && c.commandHistoryIndex < len(c.commandHistory)-1 {
					c.commandStringBeingTyped = c.commandHistory[c.commandHistoryIndex]
					c.resetCommandPrompt(c.commandStringBeingTyped)
					ui.Render(c.grid)
				}
				if c.commandHistoryIndex < len(c.commandHistory)-1 {
					c.commandHistoryIndex++
				}
			default:
				c.appendCommandPrompt(e.ID)
				ui.Render(c.grid)
			}

		case <-ticker:
			c.tableCell.Text = fmt.Sprintf("TickerCount = %d", tickerCount)

			ui.Render(c.grid)
			tickerCount++
		}
	}
}
