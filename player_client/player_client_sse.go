package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	messages "github.com/nrawrx3/uknow/internal/messages"
	utils "github.com/nrawrx3/uknow/internal/utils"
)

func (c *PlayerClient) sseController(response *http.Response) {
	c.logToWindow("connected to admin, starting SSE controller")

	lineReader := utils.NewLineReader(response.Body, c.Logger)

	lineBytes, err := io.ReadAll(lineReader)
	if err != nil {
		c.logToWindow("Unexpected error while reading first event message from admin: %v", err)
		return
	}

	firstMessage, err := messages.DecodeEvent[messages.ExistingPlayersListEvent](lineBytes)
	if err != nil {
		c.logToWindow("Failed to unmarshal messages.PlayerJoinedEvent: %v", err)
		return
	}

	// Send an ack to admin
	c.noteEachPlayer(context.Background(), firstMessage.PlayerNames, nil)
	c.logToWindow("done sending ack to admin after receiving first existing players list event message")

	c.stateMutex.Lock()
	c.clientState = WaitingForAdminToServeCards
	c.stateMutex.Unlock()

	// Now start the loop

	for {
		lineBytes, err := io.ReadAll(lineReader)
		if err != nil {
			if errors.Is(err, utils.ErrDoneReadingLines) {
				c.logToWindow("done reading all lines from admin")
				return
			}
			c.logToWindow("unexpected error while reading next line: %v", err)
			return
		}

		c.Logger.Printf("lineReader received: %s", lineBytes)

		serverEvent, err := messages.ParseServerEventMessage(lineBytes)
		if err != nil {
			c.logToWindow("Failed to parse server event message: %v", err)
			continue
		}

		c.logToWindow("received server event: %T %+v", serverEvent, serverEvent)

		// switch on event, check if current state can transition and do that

		switch ev := serverEvent.(type) {
		case messages.PlayerJoinedEvent:
			func() {
				c.stateMutex.Lock()
				defer c.stateMutex.Unlock()
				c.neighborListenAddr[ev.PlayerName] = utils.HostPortProtocol{} // Ignore, just keep the name
				c.noteEachPlayer(context.Background(), []string{ev.PlayerName}, []utils.HostPortProtocol{{}})
			}()

		case messages.ServedCardsEvent:
			func() {
				c.stateMutex.Lock()
				defer c.stateMutex.Unlock()

				if c.clientState != WaitingForAdminToServeCards {
					// TODO: Implement an admin error handler and send that error to the
					// admin. This way the admin can decide to send the client a
					// ReinitState event.
					c.Logger.Printf("Received ServedCardEvents, but client state is not %s, instead it is %s", WaitingForAdminToServeCards, c.clientState)
					return
				}

				ev.Table.LocalPlayerName = c.table.LocalPlayerName
				c.table.Set(&ev.Table)

				uiCommand := &UICommandSetServedCards{table: &ev.Table}
				err = c.sendCommandToUI(uiCommand, 1*time.Second)
				if err != nil {
					c.Logger.Print(err)
				}
				c.clientState = WaitingForAdminToChoosePlayer
			}()

		case messages.ChosenPlayerEvent:
			func() {
				c.stateMutex.Lock()
				defer c.stateMutex.Unlock()

				if c.clientState != WaitingForAdminToChoosePlayer {
					c.Logger.Printf("Received ChosenPlayerEvent, but client state is not %s, instead it is %s", WaitingForAdminToChoosePlayer, c.clientState)
					return
				}

				if c.table.LocalPlayerName == ev.PlayerName {
					c.logToWindow("↑ YOUR TURN ↑ ")
					go c.askAndRunUserDecisions(ev.DecisionEventCounter)
				} else {
					c.clientState = WaitingForDecisionSync
					c.logToWindow("PLAYER %s's TURN", ev.PlayerName)
				}
			}()

		case messages.PlayerDecisionsSyncEvent:
			func() {
				c.Logger.Printf("Received player_decisions_sync_event")
				if ev.DecidingPlayer == c.table.LocalPlayerName {
					return
				}

				c.stateMutex.Lock()
				defer c.stateMutex.Unlock()

				if c.clientState != WaitingForDecisionSync {
					c.Logger.Printf("Received PlayerDecisionsEvent, but client state is not %s, instead it is %s", WaitingForDecisionSync, c.clientState)
					return
				}

				c.Logger.Printf("Evaluating player_decisions_sync, deciding player: %s, decisions count: %d, decisions: %+v, COUNTER: %d", ev.DecidingPlayer, len(ev.Decisions), ev.Decisions, ev.DecisionEventCounter)
				c.table.EvalPlayerDecisions(ev.DecidingPlayer, ev.Decisions, c.GameEventPushChan)

				c.ackPlayerSyncToAdmin(context.Background(), ev.DecisionEventCounter)

				c.clientState = WaitingForAdminToChoosePlayer
				c.Logger.Printf("Done evaluating player %s's %d decisions, COUNTER: %d", ev.DecidingPlayer, len(ev.Decisions), ev.DecisionEventCounter)
			}()
		}
	}
}
