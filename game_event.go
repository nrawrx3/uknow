package uknow

import (
	"fmt"
	"strings"
)

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

type DrawTwoCardActionEvent struct {
	Player        string
	SkippedPlayer string
	NextPlayer    string
}

func (e *DrawTwoCardActionEvent) StringMessage(localPlayerName string) string {
	playerName := changeIfSelf(e.Player, localPlayerName)
	skippedPlayerName := changeIfSelf(e.SkippedPlayer, localPlayerName)
	nextPlayerName := changeIfSelf(e.NextPlayer, localPlayerName)

	return fmt.Sprintf("%s played a draw-2-card, skipping and adding cards to %s, making %s the next player", playerName, skippedPlayerName, nextPlayerName)
}

func (e DrawTwoCardActionEvent) GameEventName() string {
	return "Draw2ActionEvent"
}

type ReverseCardActionEvent struct {
	Player       string
	DeniedPlayer string
	NextPlayer   string
}

func (e *ReverseCardActionEvent) StringMessage(localPlayerName string) string {
	playerName := changeIfSelf(e.Player, localPlayerName)
	skippedPlayerName := changeIfSelf(e.DeniedPlayer, localPlayerName)
	nextPlayerName := changeIfSelf(e.NextPlayer, localPlayerName)

	return fmt.Sprintf("%s played a reverse-card, skipping %s and making %s the next player", playerName, skippedPlayerName, nextPlayerName)
}

func (e ReverseCardActionEvent) GameEventName() string {
	return "ReverseActionEvent"
}

func changeIfSelf(playerName, localPlayerName string) string {
	if playerName == localPlayerName {
		return fmt.Sprintf("You(%s)", localPlayerName)
	}
	return playerName
}

type WildCardActionEvent struct {
	Player string
}

func (e *WildCardActionEvent) StringMessage(localPlayerName string) string {
	playerName := changeIfSelf(e.Player, localPlayerName)
	return fmt.Sprintf("%s played a wild card", playerName)
}

func (e WildCardActionEvent) GameEventName() string {
	return "WildCardActionEvent"
}

type AwaitingWildCardColorDecisionEvent struct {
	Player                     string
	IsDraw4                    bool
	AskDecisionFromLocalPlayer bool
}

func (e *AwaitingWildCardColorDecisionEvent) StringMessage(localPlayerName string) string {
	if localPlayerName == e.Player {
		return fmt.Sprintf("Need wild card (draw4=%v) color decision from You(%s)", e.IsDraw4, localPlayerName)
	}
	return fmt.Sprintf("Need wild card color(draw4=%v) decision from %s", e.IsDraw4, e.Player)
}

func (e AwaitingWildCardColorDecisionEvent) GameEventName() string {
	if e.IsDraw4 {
		return "AwaitingWildCardColorDecisionEvent(Draw4=True)"
	}
	return "AwaitingWildCardColorDecisionEvent(Draw4=False)"
}

type WildCardColorChosenEvent struct {
	Player      string
	ChosenColor Card
}

func (e *WildCardColorChosenEvent) StringMessage(localPlayerName string) string {
	playerName := changeIfSelf(e.Player, localPlayerName)
	return fmt.Sprintf("%s chose wild card color to be %s", playerName, e.ChosenColor.String())
}

func (e WildCardColorChosenEvent) GameEventName() string {
	return "WildCardColorChosenEvent"
}

type ChallengerSuccessEvent struct {
	ChallengerName      string
	WildDraw4PlayerName string
	EligibleCards       []Card
}

func (e *ChallengerSuccessEvent) StringMessage(localPlayerName string) string {
	challengerName := changeIfSelf(e.ChallengerName, localPlayerName)
	wildPlayerName := changeIfSelf(e.WildDraw4PlayerName, localPlayerName)

	var sb strings.Builder

	for _, c := range e.EligibleCards {
		sb.WriteString(c.SymbolString())
		sb.WriteRune(' ')
	}

	return fmt.Sprintf("%s successfully challenged %s. %s had the following eligible cards he could play: %v", challengerName, wildPlayerName, wildPlayerName, sb.String())
}

func (e ChallengerSuccessEvent) GameEventName() string {
	return "ChallengerSuccessEvent"
}

type ChallengerFailedEvent struct {
	ChallengerName      string
	WildDraw4PlayerName string
}

func (e *ChallengerFailedEvent) StringMessage(localPlayerName string) string {
	challengerName := changeIfSelf(e.ChallengerName, localPlayerName)
	wildPlayerName := changeIfSelf(e.WildDraw4PlayerName, localPlayerName)
	return fmt.Sprintf("%s un-successfully challenged %s", challengerName, wildPlayerName)
}

func (e ChallengerFailedEvent) GameEventName() string {
	return "ChallengerFailedEvent"
}
