package uknow

import "fmt"

type CardTransferNode string

const (
	CardTransferNodeDeck       CardTransferNode = "deck"
	CardTransferNodePile       CardTransferNode = "pile"
	CardTransferNodePlayerHand CardTransferNode = "player_hand"
)

type CardTransferEvent struct {
	Source       CardTransferNode
	Sink         CardTransferNode
	SourcePlayer string // If applicable
	SinkPlayer   string // If applicable
}

func (c *CardTransferEvent) String() string {
	sourceName := string(c.Source)
	if c.Source == CardTransferNodePlayerHand {
		sourceName = "player " + c.SourcePlayer
	}

	sinkName := string(c.Sink)
	if c.Sink == CardTransferNodePlayerHand {
		sinkName = "player " + c.SinkPlayer
	}

	return fmt.Sprintf("Card transfer from %s to %s", sourceName, sinkName)
}
