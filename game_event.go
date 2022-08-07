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

	// Used by UI to avoid updating widgets like command-prompt in case
	// event source is from a non-local client
	FromLocalClient() bool
}

type CardTransferEvent struct {
	Source            CardTransferNode
	Sink              CardTransferNode
	SourcePlayer      string // If applicable
	SinkPlayer        string // If applicable
	Card              Card
	IsFromLocalClient bool
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

	sourceName, _ = changeIfSelf(sourceName, localPlayerName)
	sinkName, _ = changeIfSelf(sinkName, localPlayerName)

	return fmt.Sprintf("Card transfer from %s to %s", sourceName, sinkName)
}

func (c CardTransferEvent) FromLocalClient() bool {
	return c.IsFromLocalClient
}

func (c CardTransferEvent) GameEventName() string {
	return "CardTransferEvent"
}

type SkipCardActionEvent struct {
	Player            string
	SkippedPlayer     string
	NextPlayer        string
	IsFromLocalClient bool
}

func (e *SkipCardActionEvent) StringMessage(localPlayerName string) string {
	playerName, _ := changeIfSelf(e.Player, localPlayerName)
	skippedPlayerName, _ := changeIfSelf(e.SkippedPlayer, localPlayerName)
	nextPlayerName, _ := changeIfSelf(e.NextPlayer, localPlayerName)

	return fmt.Sprintf("%s played a skip-card, skipping %s, making %s the next player", playerName, skippedPlayerName, nextPlayerName)
}

func (e SkipCardActionEvent) GameEventName() string {
	return "SkipActionEvent"
}

func (e SkipCardActionEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

type DrawTwoCardActionEvent struct {
	Player            string
	SkippedPlayer     string
	NextPlayer        string
	IsFromLocalClient bool
}

func (e *DrawTwoCardActionEvent) StringMessage(localPlayerName string) string {
	playerName, _ := changeIfSelf(e.Player, localPlayerName)
	skippedPlayerName, _ := changeIfSelf(e.SkippedPlayer, localPlayerName)
	nextPlayerName, _ := changeIfSelf(e.NextPlayer, localPlayerName)

	return fmt.Sprintf("%s played a draw-2-card, skipping and adding cards to %s, making %s the next player", playerName, skippedPlayerName, nextPlayerName)
}

func (e DrawTwoCardActionEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e DrawTwoCardActionEvent) GameEventName() string {
	return "Draw2ActionEvent"
}

type ReverseCardActionEvent struct {
	Player            string
	DeniedPlayer      string
	NextPlayer        string
	IsFromLocalClient bool
}

func (e *ReverseCardActionEvent) StringMessage(localPlayerName string) string {
	playerName, _ := changeIfSelf(e.Player, localPlayerName)
	skippedPlayerName, _ := changeIfSelf(e.DeniedPlayer, localPlayerName)
	nextPlayerName, _ := changeIfSelf(e.NextPlayer, localPlayerName)

	return fmt.Sprintf("%s played a reverse-card, skipping %s and making %s the next player", playerName, skippedPlayerName, nextPlayerName)
}

func (e ReverseCardActionEvent) GameEventName() string {
	return "ReverseActionEvent"
}

func (e ReverseCardActionEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func changeIfSelf(playerName, localPlayerName string) (string, bool) {
	if playerName == localPlayerName {
		return fmt.Sprintf("You(%s)", localPlayerName), true
	}
	return playerName, false
}

type WildCardActionEvent struct {
	Player            string
	IsFromLocalClient bool
}

func (e *WildCardActionEvent) StringMessage(localPlayerName string) string {
	playerName, _ := changeIfSelf(e.Player, localPlayerName)
	return fmt.Sprintf("%s played a wild card", playerName)
}

func (e WildCardActionEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e WildCardActionEvent) GameEventName() string {
	return "WildCardActionEvent"
}

type AwaitingWildCardColorDecisionEvent struct {
	Player                     string
	IsDraw4                    bool
	AskDecisionFromLocalPlayer bool
	IsFromLocalClient          bool
}

func (e *AwaitingWildCardColorDecisionEvent) StringMessage(localPlayerName string) string {
	if localPlayerName == e.Player {
		return fmt.Sprintf("Need wild card (draw4=%v) color decision from You(%s)", e.IsDraw4, localPlayerName)
	}
	return fmt.Sprintf("Need wild card color(draw4=%v) decision from %s", e.IsDraw4, e.Player)
}

func (e AwaitingWildCardColorDecisionEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e AwaitingWildCardColorDecisionEvent) GameEventName() string {
	if e.IsDraw4 {
		return "AwaitingWildCardColorDecisionEvent(Draw4=True)"
	}
	return "AwaitingWildCardColorDecisionEvent(Draw4=False)"
}

type WildCardColorChosenEvent struct {
	Player            string
	ChosenColor       Card
	IsFromLocalClient bool
}

func (e *WildCardColorChosenEvent) StringMessage(localPlayerName string) string {
	playerName, _ := changeIfSelf(e.Player, localPlayerName)
	return fmt.Sprintf("%s chose wild card color to be %s", playerName, e.ChosenColor.String())
}

func (e WildCardColorChosenEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e WildCardColorChosenEvent) GameEventName() string {
	return "WildCardColorChosenEvent"
}

type ChallengerSuccessEvent struct {
	ChallengerName      string
	WildDraw4PlayerName string
	EligibleCards       []Card
	IsFromLocalClient   bool
}

func (e *ChallengerSuccessEvent) StringMessage(localPlayerName string) string {
	challengerName, _ := changeIfSelf(e.ChallengerName, localPlayerName)
	wildPlayerName, _ := changeIfSelf(e.WildDraw4PlayerName, localPlayerName)

	var sb strings.Builder

	for _, c := range e.EligibleCards {
		sb.WriteString(c.SymbolString())
		sb.WriteRune(' ')
	}

	return fmt.Sprintf("%s successfully challenged %s. %s had the following eligible cards he could play: %v", challengerName, wildPlayerName, wildPlayerName, sb.String())
}

func (e ChallengerSuccessEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e ChallengerSuccessEvent) GameEventName() string {
	return "ChallengerSuccessEvent"
}

type ChallengerFailedEvent struct {
	ChallengerName      string
	WildDraw4PlayerName string
	IsFromLocalClient   bool
}

func (e *ChallengerFailedEvent) StringMessage(localPlayerName string) string {
	challengerName, _ := changeIfSelf(e.ChallengerName, localPlayerName)
	wildPlayerName, _ := changeIfSelf(e.WildDraw4PlayerName, localPlayerName)
	return fmt.Sprintf("%s un-successfully challenged %s", challengerName, wildPlayerName)
}

func (e ChallengerFailedEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e ChallengerFailedEvent) GameEventName() string {
	return "ChallengerFailedEvent"
}

type AwaitingPlayOrPassEvent struct {
	Player                     string
	AskDecisionFromLocalPlayer bool
	IsFromLocalClient          bool
}

func (e *AwaitingPlayOrPassEvent) StringMessage(localPlayerName string) string {
	playerName, _ := changeIfSelf(e.Player, localPlayerName)
	return fmt.Sprintf("awaiting for card play or pass from %s", playerName)
}

func (e AwaitingPlayOrPassEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e AwaitingPlayOrPassEvent) GameEventName() string {
	return "AwaitingPlayOrPassEvent"
}

type PlayerPassedTurnEvent struct {
	Player            string
	PlayerOfNextTurn  string
	IsFromLocalClient bool
}

func (e *PlayerPassedTurnEvent) StringMessage(localPlayerName string) string {
	playerName, _ := changeIfSelf(e.Player, localPlayerName)
	return fmt.Sprintf("%s passed turn, next player is %s", playerName, e.PlayerOfNextTurn)
}

func (e PlayerPassedTurnEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e PlayerPassedTurnEvent) GameEventName() string {
	return "PlayerPassedTurnEvent"
}

type PlayerHasWonEvent struct {
	Player            string
	IsFromLocalClient bool
}

func (e *PlayerHasWonEvent) StringMessage(localPlayerName string) string {
	playerName, you := changeIfSelf(e.Player, localPlayerName)
	if you {
		return fmt.Sprintf("%s are the winner", playerName)
	}
	return fmt.Sprintf("%s is the winner", playerName)
}

func (e PlayerHasWonEvent) FromLocalClient() bool {
	return e.IsFromLocalClient
}

func (e PlayerHasWonEvent) GameEventName() string {
	return "PlayerHasWonEvent"
}
