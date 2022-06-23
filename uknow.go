package uknow

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

type Number int

type Card struct {
	Number Number
	Color  Color
}

func (c *Card) String() string {
	return fmt.Sprintf("%s of %s", c.Number.String(), c.Color.String())
}

func (c Card) IsLessThan(c1 Card) bool {
	return c.Color < c1.Color || c.Number < c1.Number
}

func (c Card) IsEqual(c1 Card) bool {
	return c.Color == c1.Color && c.Number == c1.Number
}

// Special cards
const (
	NumberSkip         Number = 10 + iota
	NumberReverse      Number = 10 + iota
	NumberDrawTwo      Number = 10 + iota
	NumberWild         Number = 10 + iota
	NumberWildDrawFour Number = 10 + iota
)

func (num Number) IsAction() bool {
	return NumberSkip <= num && num <= NumberWildDrawFour
}

func (c Card) IsWild() bool {
	return c.Number == NumberWild || c.Number == NumberWildDrawFour
}

func (num *Number) String() string {
	n := *num

	if 0 <= n && n <= 9 {
		return fmt.Sprintf("%d", n)
	}

	switch n {
	case NumberSkip:
		return "Skip"
	case NumberReverse:
		return "Reverse"
	case NumberDrawTwo:
		return "DrawTwo"
	case NumberWild:
		return "Wild"
	case NumberWildDrawFour:
		return "WildDrawFour"
	default:
		return fmt.Sprintf("invalid_number(= %d)", n)
	}
}

func IntToNumber(n int) (Number, error) {
	if 0 <= n && n <= int(NumberWildDrawFour) {
		return Number(n), nil
	}
	return 0, fmt.Errorf("InvalidCardNumber(%d)", n)
}

type Color int

const (
	Wild   Color = 0
	Red    Color = 1
	Green  Color = 2
	Blue   Color = 3
	Yellow Color = 4
)

var ColorSymbol = [...]string{
	"🌈",
	"🔴",
	"💚",
	"🔵",
	"💛",
}

var ActionSymbol = [...]string{
	"⊘",
	"↺",
	"⧺",
	"⓪",
	"➍",
}

func (c *Card) SymbolString() string {
	if !c.Number.IsAction() {
		return fmt.Sprintf("⟨%s.%s⟩", ColorSymbol[c.Color], c.Number.String())
	}

	return fmt.Sprintf("⟨%s.%s⟩", ColorSymbol[c.Color], ActionSymbol[int(c.Number)-int(NumberSkip)])
}

func (c *Color) String() string {
	switch *c {
	case 1:
		return "red"
	case 2:
		return "green"
	case 3:
		return "blue"
	case 4:
		return "yellow"
	case 0:
		return "wild"
	default:
		return "invalid_color"
	}
}

type Deck []Card

func (d Deck) String() string {
	if len(d) == 0 {
		return "[]"
	}

	var sb strings.Builder

	sb.WriteString("[")

	for _, card := range d[0 : len(d)-1] {
		sb.WriteString(card.String())
		sb.WriteString("|")
	}

	sb.WriteString(d[len(d)-1].String())
	sb.WriteString("]")

	return sb.String()
}

func (d Deck) Len() int {
	return len(d)
}

func (d Deck) Less(i, j int) bool {
	return d[i].IsLessThan(d[j]) || (i < j)
}

func (d Deck) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}

func (d Deck) Clone() Deck {
	newDeck := make(Deck, len(d), cap(d))
	copy(newDeck, d)
	return newDeck
}

func NewEmptyDeck() Deck {
	return make([]Card, 0, 1024)
}

func NewFullDeck() Deck {
	// Non zero cards upto CardDrawTwo, 9 of them for each color
	cards := make([]Card, 0, 9*8)
	for color := 1; color <= 4; color++ {
		for number := 1; number <= int(NumberDrawTwo); number++ {
			cards = append(cards, Card{Number: Number(number), Color: Color(color)})
		}
	}

	// Two copies of non-zero cards for each color
	cards = append(cards, cards...)

	// Zero cards are only one per color
	for color := 1; color <= 4; color++ {
		cards = append(cards, Card{Number: 0, Color: Color(color)})
	}

	// 4 NumberWild and 4 NumberWildDrawFour cards
	for i := 0; i < 4; i++ {
		cards = append(cards, Card{Number: NumberWildDrawFour, Color: Wild})
		cards = append(cards, Card{Number: NumberWild, Color: Wild})
	}

	deck := Deck(cards)
	sort.Sort(deck)
	return deck
}

var ErrEmptyDeck = errors.New("empty deck")

func (d Deck) IsEmpty() bool {
	return len(d) == 0
}

func (d Deck) Push(c ...Card) Deck {
	return append(d, c...)
}

func (d Deck) Top() (Card, error) {
	if d.IsEmpty() {
		return Card{}, ErrEmptyDeck
	}
	return d[len(d)-1], nil
}

func (d Deck) MustTop() Card {
	if d.IsEmpty() {
		panic("Deck.MustTop() called on empty deck")
	}
	return d[len(d)-1]
}

func (d Deck) Pop() (Deck, error) {
	if d.IsEmpty() {
		return d, ErrEmptyDeck
	}
	return d[0 : len(d)-1], nil
}

func (d Deck) MustPop() Deck {
	if d.IsEmpty() {
		panic("Deck.MustPop() called on an empty deck")
	}
	return d[0 : len(d)-1]
}

func (d Deck) RemoveCard(index int) Deck {
	return append(d[0:index], d[index+1:]...)
}

func (d Deck) FindCard(wantedCard Card) (int, error) {
	for i, card := range d {
		if card.Number == wantedCard.Number && card.Color == wantedCard.Color {
			return i, nil
		}
	}
	return 0, fmt.Errorf("could not find card %s", wantedCard.String())
}

func (d Deck) FindAndRemoveCard(wantedCard Card) (Deck, error) {
	index, err := d.FindCard(wantedCard)
	if err != nil {
		return d, fmt.Errorf("could not remove card: %w", err)
	}
	return d.RemoveCard(index), nil
}

func (d Deck) MustFindCard(wantedCard Card) int {
	for i, card := range d {
		if card.Number == wantedCard.Number && card.Color == wantedCard.Color {
			return i
		}
	}
	panic(fmt.Sprintf("Could not find card '%s' in given deck", wantedCard.String()))
}

type TurnStateBits uint64

const (
	TurnStateStart      TurnStateBits = 1 << iota
	TurnStateCardDrawn  TurnStateBits = 1 << iota
	TurnStateCardPlayed TurnStateBits = 1 << iota
)

func (stateBits TurnStateBits) HasFlag(flagBit TurnStateBits) bool {
	return (stateBits & flagBit) != 0
}

func (stateBits TurnStateBits) SetFlag(flagBit TurnStateBits) TurnStateBits {
	return stateBits | flagBit
}

type Table struct {
	Logger *log.Logger `json:"-"`

	DrawDeck         Deck            `json:"draw_deck"`
	DiscardedPile    Deck            `json:"discarded_pile"`
	IndexOfPlayer    map[string]int  `json:"index_of_player"`
	HandOfPlayer     map[string]Deck `json:"hand_of_player"`
	PlayerNames      StringSlice     `json:"player_names"`
	LocalPlayerName  string          `json:"local_player_name"`
	ShufflerName     string          `json:"shuffler_name"`
	PlayerOfNextTurn string          `json:"player_of_next_turn"`
	Direction        int             `json:"direction"`
	TurnsCompleted   int             `json:"turns_completed"`
	PlayerTurnState  TurnStateBits   `json:"turn_state_bits"`
	IsShuffled       bool            `json:"is_shuffled"`
	RequiredColor    Color           `json:"required_color"`
	// ^ We cannot determine elligible play card color simply from top of
	// discard pile since wild card player enfoces a specific color for next
	// player. This field is to be kept in sync whenever a player discards a
	// card
}

func NewTable(localPlayerName string, logger *log.Logger) *Table {
	table := createNewTable(logger)
	table.LocalPlayerName = localPlayerName
	table.AddPlayer(localPlayerName)
	return table
}

// Shallow-copies other into the receiver table
func (t *Table) Set(other *Table) {
	t.DrawDeck = other.DrawDeck
	t.DiscardedPile = other.DiscardedPile
	t.IndexOfPlayer = other.IndexOfPlayer
	t.HandOfPlayer = other.HandOfPlayer
	t.PlayerNames = other.PlayerNames
	// NOTE: Not copying local player name since it doesn't make sense.
	// CONSIDER: In fact, we could get rid of the LocalPlayerName field altogether and pass it around instead.
	t.ShufflerName = other.ShufflerName
	t.PlayerOfNextTurn = other.PlayerOfNextTurn
	t.Direction = other.Direction
	t.IsShuffled = other.IsShuffled
}

func NewAdminTable(logger *log.Logger) *Table {
	return createNewTable(logger)
}

func (t *Table) Summary() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("DrawDeck count: %d\n", t.DrawDeck.Len()))
	sb.WriteString(fmt.Sprintf("Pile count: %d\n", t.DiscardedPile.Len()))
	sb.WriteString("Hand counts, Index:\n----------\n")
	for playerName, hand := range t.HandOfPlayer {
		sb.WriteString(fmt.Sprintf("%s: %d, %d\n", playerName, hand.Len(), t.IndexOfPlayer[playerName]))
	}
	sb.WriteString(fmt.Sprintf("Shuffler: %s\n", t.ShufflerName))
	sb.WriteString(fmt.Sprintf("NextPlayerToDraw: %s\n", t.PlayerOfNextTurn))
	sb.WriteString(fmt.Sprintf("Direction: %d\n", t.Direction))

	return sb.String()
}

func createNewTable(logger *log.Logger) *Table {
	return &Table{
		DrawDeck:        NewFullDeck(),
		DiscardedPile:   NewEmptyDeck(),
		HandOfPlayer:    make(map[string]Deck),
		IndexOfPlayer:   make(map[string]int),
		PlayerNames:     make([]string, 0, 16),
		Direction:       1,
		Logger:          logger,
		PlayerTurnState: TurnStateStart,
	}
}

func (t *Table) IsServerTable() bool {
	return t.LocalPlayerName == ""
}

var ErrPlayerAlreadyExists = errors.New("player already exists")

func (t *Table) AddPlayer(playerName string) error {
	for _, existingName := range t.PlayerNames {
		if existingName == playerName {
			return errors.New("existing player has same name as to-be-added player")
		}
	}

	t.PlayerNames = append(t.PlayerNames, playerName)
	t.IndexOfPlayer[playerName] = len(t.PlayerNames) - 1
	t.HandOfPlayer[playerName] = NewEmptyDeck()
	return nil
}

func (t *Table) PlayerIndicesSortedByTurn() []int {
	sortedIndices := make([]int, t.PlayerCount())
	curIndex := t.IndexOfPlayer[t.PlayerOfNextTurn]
	for i := 0; i < t.PlayerCount(); i++ {
		sortedIndices[i] = curIndex
		curIndex = t.GetNextPlayerIndex(curIndex, 1)
	}
	return sortedIndices
}

func (t *Table) PlayerCount() int {
	return len(t.PlayerNames)
}

func (t *Table) GetNextPlayerIndex(curPlayerIndex int, step int) int {
	i := (curPlayerIndex + t.Direction*step) % t.PlayerCount()
	if i < 0 {
		return t.PlayerCount() + i
	}
	return i
}

func (t *Table) PlayerIndexFromName(playerName string) int {
	for i, name := range t.PlayerNames {
		if name == playerName {
			return i
		}
	}

	panic(fmt.Errorf("Non-existent player name: '%s'", playerName))
}

func (t *Table) SetIndexOfPlayer(indexOfPlayer map[string]int) error {
	for playerName, index := range indexOfPlayer {
		_, exists := t.IndexOfPlayer[playerName]
		if !exists {
			return fmt.Errorf("Player %s does not exist in table", playerName)
		}
		t.IndexOfPlayer[playerName] = index
		t.PlayerNames[index] = playerName
	}

	return nil
}

func (t *Table) RearrangePlayerIndices(indices []int) {
	sort.Sort(t.PlayerNames)

	for i, j := range indices {
		t.PlayerNames[i], t.PlayerNames[j] = t.PlayerNames[j], t.PlayerNames[i]
		t.IndexOfPlayer[t.PlayerNames[i]] = i
		t.IndexOfPlayer[t.PlayerNames[j]] = j
	}
}

func (t *Table) ShuffleDeckAndDistribute(startingHandCount int) {
	if t.IsShuffled {
		t.Logger.Printf("WARNING: Already shuffled deck")
	}

	if startingHandCount <= 0 || startingHandCount > 12 {
		panic("Let's not use too large of a starting hand count")
	}

	deckSize := len(t.DrawDeck)
	shuffledIndices := ShuffleIntRange(0, deckSize)

	for i, j := range shuffledIndices {
		t.DrawDeck.Swap(i, j)
	}

	// Distribute
	for playerName := range t.IndexOfPlayer {
		t.HandOfPlayer[playerName] = t.DrawDeck[0:startingHandCount]
		t.DrawDeck = t.DrawDeck[startingHandCount:len(t.DrawDeck)]
	}

	topCard, _ := t.DrawDeck.Top()
	t.DrawDeck = t.DrawDeck.MustPop()
	t.DiscardedPile = t.DiscardedPile.Push(topCard)

	t.Logger.Printf("Top card: %+v", topCard)

	if topCard.Number == NumberReverse {
		t.Direction = -t.Direction
	}

	if topCard.IsWild() {
		// TODO(@rk): Make player choose the color if wild
		t.RequiredColor = Red
	} else {
		t.RequiredColor = topCard.Color
	}

	indexOfNextPlayer := t.GetNextPlayerIndex(t.IndexOfPlayer[t.ShufflerName], 1)
	t.PlayerOfNextTurn = t.PlayerNames[indexOfNextPlayer]

	t.IsShuffled = true
}

type PlayerDecisionKind int

//go:generate stringer -type=PlayerDecisionKind
const (
	PlayerDecisionPullFromDeck PlayerDecisionKind = iota + 1
	PlayerDecisionPullFromPile
	PlayerDecisionPlayHandCard
)

type PlayerDecision struct {
	Kind       PlayerDecisionKind
	ResultCard Card // Only required when Kind == PlayerDecisionPlayHandCard
}

func (e *PlayerDecision) String() string {
	resultCard := ""
	if e.Kind == PlayerDecisionPlayHandCard {
		resultCard = ": " + e.ResultCard.String()
	}
	return fmt.Sprintf("%s%s", e.Kind.String(), resultCard)
}

func (t *Table) EvalPlayerDecisionsNoTransferChan(decidingPlayer string, decisions []PlayerDecision) {
	dummyTransferChan := make(chan CardTransferEvent)
	go func() {
		for range dummyTransferChan {
		}
	}()
	t.EvalPlayerDecisions(decidingPlayer, decisions, dummyTransferChan)
}

func (t *Table) EvalPlayerDecisions(decidingPlayer string, decisions []PlayerDecision, transferEventsChan chan<- CardTransferEvent) {
	for _, decision := range decisions {
		t.EvalPlayerDecision(decidingPlayer, decision, transferEventsChan)
	}
}

type EvalDecisionError struct {
	Decision PlayerDecision
	Reason   error
}

var ErrCardNotInHand = errors.New("card not in hand")
var ErrAlreadyDrewCard = errors.New("already drew card this turn")
var ErrDrawDeckIsEmpty = errors.New("draw-deck is empty")
var ErrDiscardPileIsEmpty = errors.New("discard pile is empty")
var ErrUnknownPlayer = errors.New("unknown player")
var ErrIllegalPlayCard = errors.New("card illegal")

func (e *EvalDecisionError) Error() string {
	return fmt.Sprintf("%s: %s", e.Reason.Error(), e.Decision.String())
}

// TODO(@rk): Incomplete. Takes a decision event, "evaluates" the bare minimum,
// i.e. update deck/pile/hand of the decision player and pushes the event to the
// transferEventChan. We need to do the whole "game logic" in this function.
// LIST THE NEXT ELIGIBLE CARDS THAT CAN BE PLAYED BY THE PLAYER
func (t *Table) EvalPlayerDecision(decidingPlayer string, decision PlayerDecision, transferEventsChan chan<- CardTransferEvent) (PlayerDecision, error) {
	handOfPlayer := t.HandOfPlayer[decidingPlayer]

	switch decision.Kind {
	case PlayerDecisionPullFromDeck:
		if t.PlayerTurnState.HasFlag(TurnStateCardDrawn) {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrAlreadyDrewCard}
		}

		topCard, err := t.DrawDeck.Top()
		if err != nil {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrDrawDeckIsEmpty}
		}

		t.HandOfPlayer[decidingPlayer] = handOfPlayer.Push(topCard)
		t.DrawDeck = t.DrawDeck.MustPop()

		transferEventsChan <- CardTransferEvent{
			Source:     CardTransferNodeDeck,
			Sink:       CardTransferNodePlayerHand,
			SinkPlayer: decidingPlayer,
			Card:       topCard,
		}

		decision.ResultCard = topCard
		t.PlayerTurnState = t.PlayerTurnState.SetFlag(TurnStateCardDrawn)

	case PlayerDecisionPullFromPile:
		if t.PlayerTurnState.HasFlag(TurnStateCardDrawn) {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrAlreadyDrewCard}
		}

		topCard, err := t.DiscardedPile.Top()
		if err != nil {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrDiscardPileIsEmpty}
		}
		t.HandOfPlayer[decidingPlayer] = handOfPlayer.Push(topCard)
		t.DiscardedPile = t.DiscardedPile.MustPop()

		transferEventsChan <- CardTransferEvent{
			Source:     CardTransferNodePile,
			Sink:       CardTransferNodePlayerHand,
			SinkPlayer: decidingPlayer,
			Card:       topCard,
		}

		decision.ResultCard = topCard
		t.PlayerTurnState = t.PlayerTurnState.SetFlag(TurnStateCardDrawn)

	case PlayerDecisionPlayHandCard:
		t.TryPlayCard(decidingPlayer, decision.ResultCard, transferEventsChan)
	}

	// TODO(@rk): This also depends on the logic. Simply moving to next player
	playerIndex := t.PlayerIndexFromName(decidingPlayer)
	nextPlayerIndex := t.GetNextPlayerIndex(playerIndex, t.Direction)
	t.PlayerOfNextTurn = t.PlayerNames[nextPlayerIndex]
	t.PlayerTurnState = TurnStateStart

	t.Logger.Printf("Evaluated decision: %s\n\tfrom player: %s,\n\tplayer of next turn: %s", decision.String(), decidingPlayer, t.PlayerOfNextTurn)

	return decision, nil
}

// TODO(@rk): Evaluate the played card, emitting more transfer events and deciding NextPlayerToDraw

// CONSIDER(@rk): For replay events, we shouldn't need to check rules.
func (t *Table) TryPlayCard(playerName string, cardToPlay Card, transferEventsChan chan<- CardTransferEvent) error {
	// This procedure's precondition is that it was indeed the player's turn. Given that, it checks if the play is valid
	decision := PlayerDecision{
		Kind:       PlayerDecisionPlayHandCard,
		ResultCard: cardToPlay,
	}

	// cardToPlay must come from hand
	playerHand, ok := t.HandOfPlayer[playerName]
	if !ok {
		return &EvalDecisionError{
			Decision: decision,
			Reason:   fmt.Errorf("%w: %s", ErrUnknownPlayer, playerName),
		}
	}

	_, err := playerHand.FindCard(cardToPlay)
	if err != nil {
		return &EvalDecisionError{
			Decision: decision,
			Reason:   ErrCardNotInHand,
		}
	}

	// Check top of discard pile
	topOfPile, err := t.DiscardedPile.Top()
	if err != nil {
		panic("Should be unreachable. Discard pile always has at least 1 card")
	}

	numberMatches := topOfPile.Number == cardToPlay.Number || cardToPlay.IsWild()
	colorMatches := t.RequiredColor == cardToPlay.Color

	if !numberMatches && !colorMatches {
		return &EvalDecisionError{
			Decision: decision,
			Reason: fmt.Errorf("%w: %s, (color should be: %s OR number should be %s)",
				ErrIllegalPlayCard,
				cardToPlay.String(),
				t.RequiredColor.String(),
				topOfPile.Number.String(),
			),
		}
	}

	// Can play card

	// Remove card from hand and put it on pile
	hand := t.HandOfPlayer[playerName]
	cardLoc := hand.MustFindCard(cardToPlay)
	hand = append(hand[0:cardLoc], hand[cardLoc+1:]...)
	t.HandOfPlayer[playerName] = hand
	t.DiscardedPile = t.DiscardedPile.Push(cardToPlay)

	transferEventsChan <- CardTransferEvent{
		Source:       CardTransferNodePlayerHand,
		Sink:         CardTransferNodePile,
		SourcePlayer: playerName,
	}

	return nil
}
