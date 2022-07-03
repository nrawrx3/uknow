package uknow

import "fmt"

type CardTransferNode string

const (
	CardTransferNodeDeck       CardTransferNode = "deck"
	CardTransferNodePile       CardTransferNode = "pile"
	CardTransferNodePlayerHand CardTransferNode = "player_hand"
)

// TODO(@rk): The UI not only needs card transfer events, but other events like
// "player emptied hands event", "player has finished at Nth place event". In
// other words, game actions being performed are not just card transfer events.
// Refactor the channels and eval functions so that they are of type GameEvent.
type GameEvent interface {
	GameEventName() string
}

type CardTransferEvent struct {
	Source       CardTransferNode
	Sink         CardTransferNode
	SourcePlayer string // If applicable
	SinkPlayer   string // If applicable
	Card         Card
}

func (c *CardTransferEvent) String(localPlayerName string) string {
	sourceName := string(c.Source)
	if c.Source == CardTransferNodePlayerHand {
		sourceName = "player " + c.SourcePlayer
	}

	sinkName := string(c.Sink)
	if c.Sink == CardTransferNodePlayerHand {
		sinkName = "player " + c.SinkPlayer
	}

	sourceName = changeIfSelf(sourceName, localPlayerName)
	sinkName = changeIfSelf(sinkName, localPlayerName)

	return fmt.Sprintf("Card transfer from %s to %s", sourceName, sinkName)
}

func (c CardTransferEvent) GameEventName() string {
	return "CardTransferEvent"
}

type SkipCardActionEvent struct {
	Player        string
	SkippedPlayer string
	NextPlayer    string
}

func (e *SkipCardActionEvent) StringMessage(localPlayerName string) string {
	playerName := changeIfSelf(e.Player, localPlayerName)
	skippedPlayerName := changeIfSelf(e.SkippedPlayer, localPlayerName)
	nextPlayerName := changeIfSelf(e.NextPlayer, localPlayerName)

	return fmt.Sprintf("%s played a skip-card, skipping %s, making %s the next player", playerName, skippedPlayerName, nextPlayerName)
}

func (e SkipCardActionEvent) GameEventName() string {
	return "SkipActionEvent"
}

func changeIfSelf(playerName, localPlayerName string) string {
	if playerName == localPlayerName {
		return fmt.Sprintf("You(%s)", localPlayerName)
	}
	return playerName
}
