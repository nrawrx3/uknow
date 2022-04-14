package uknow

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

var ErrorCouldNotFindCard = "could not find card in given deck"

type Card struct {
	Number Number
	Color  Color
}

func (c *Card) String() string {
	return fmt.Sprintf("%s of %s", c.Number.String(), c.Color.String())
}

// Special cards
const (
	CardSkip = iota + 10
	CardReverse
	CardDrawTwo
	CardWild
	CardWildDrawFour
)

type Number int

func (num Number) IsAction() bool {
	return CardSkip <= num && num <= CardWildDrawFour
}

func (num *Number) String() string {
	n := *num

	if 0 <= n && n <= 9 {
		return fmt.Sprintf("%d", n)
	}

	switch n {
	case CardSkip:
		return "Skip"
	case CardReverse:
		return "Reverse"
	case CardDrawTwo:
		return "DrawTwo"
	case CardWild:
		return "Wild"
	case CardWildDrawFour:
		return "WildDrawFour"
	default:
		return fmt.Sprintf("invalid_number(= %d)", n)
	}
}

func IntToNumber(n int) (Number, error) {
	if 0 <= n && n <= CardWildDrawFour {
		return Number(n), nil
	}
	return 0, fmt.Errorf("InvalidCardNumber(%d)", n)
}

type Color int

const (
	Red Color = 1 + iota
	Green
	Blue
	Yellow
)

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
	return d[i].Color < d[j].Color || (d[i].Number < d[j].Number) || (i < j)
}

func (d Deck) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}

func NewEmptyDeck() Deck {
	return make([]Card, 0, 1024)
}

func NewFullDeck() Deck {
	// Non zero cards upto CardDrawTwo, 9 of them for each color
	cards := make([]Card, 0, 9*8)
	for color := 1; color <= 4; color++ {
		for number := 1; number <= CardDrawTwo; number++ {
			cards = append(cards, Card{Number: Number(number), Color: Color(color)})
		}
	}

	// Two copies of non-zero cards for each color
	cards = append(cards, cards...)

	// Zero cards are only one per color
	for color := 1; color <= 4; color++ {
		cards = append(cards, Card{Number: 0, Color: Color(color)})
	}

	// 4 CardWild and 4 CardWildDrawFour
	for i := 0; i < 4; i++ {
		cards = append(cards, Card{Number: CardWildDrawFour, Color: 0})
		cards = append(cards, Card{Number: CardWild, Color: 0})
	}

	deck := Deck(cards)
	sort.Sort(deck)
	return deck
}

func (d Deck) IsEmpty() bool {
	return len(d) == 0
}

func (d Deck) Push(c Card) Deck {
	return append(d, c)
}

func (d Deck) Top() (Card, error) {
	if d.IsEmpty() {
		return Card{}, errors.New("Empty deck")
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
		return d, errors.New("Empty deck")
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

type Table struct {
	DrawDeck         Deck            `json:"draw_deck"`
	Pile             Deck            `json:"pile"`
	IndexOfPlayer    map[string]int  `json:"index_of_player"`
	HandOfPlayer     map[string]Deck `json:"hand_of_player"`
	PlayerNames      StringSlice     `json:"player_names"`
	LocalPlayerName  string          `json:"local_player_name"`
	ShufflerName     string          `json:"shuffler_name"`
	NextPlayerToDraw string          `json:"next_player_to_draw"`
	Direction        int             `json:"direction"`
	Logger           *log.Logger
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
	t.Pile = other.Pile
	t.IndexOfPlayer = other.IndexOfPlayer
	t.HandOfPlayer = other.HandOfPlayer
	t.PlayerNames = other.PlayerNames
	// NOTE: Not copying local player name since it doesn't make sense.
	// CONSIDER: In fact, we could get rid of the LocalPlayerName field altogether and pass it around instead.
	t.ShufflerName = other.ShufflerName
	t.NextPlayerToDraw = other.NextPlayerToDraw
	t.Direction = other.Direction
}

func NewAdminTable(logger *log.Logger) *Table {
	return createNewTable(logger)
}

func (t *Table) Summary() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("DrawDeck count: %d\n", t.DrawDeck.Len()))
	sb.WriteString(fmt.Sprintf("Pile count: %d\n", t.Pile.Len()))
	sb.WriteString("Hand counts, Index:\n----------\n")
	for playerName, hand := range t.HandOfPlayer {
		sb.WriteString(fmt.Sprintf("%s: %d, %d\n", playerName, hand.Len(), t.IndexOfPlayer[playerName]))
	}
	sb.WriteString(fmt.Sprintf("Shuffler: %s\n", t.ShufflerName))
	sb.WriteString(fmt.Sprintf("NextPlayerToDraw: %s\n", t.NextPlayerToDraw))
	sb.WriteString(fmt.Sprintf("Direction: %d\n", t.Direction))

	return sb.String()
}

func createNewTable(logger *log.Logger) *Table {
	return &Table{
		DrawDeck:      NewFullDeck(),
		Pile:          NewEmptyDeck(),
		HandOfPlayer:  make(map[string]Deck),
		IndexOfPlayer: make(map[string]int),
		PlayerNames:   make([]string, 0, 16),
		Direction:     1,
		Logger:        logger,
	}
}

func (t *Table) IsServerTable() bool {
	return t.LocalPlayerName == ""
}

var PlayerAlreadyExists error = errors.New("Player already exists")

func (t *Table) AddPlayer(playerName string) error {
	for _, existingName := range t.PlayerNames {
		if existingName == playerName {
			return errors.New("Existing player has same name as to-be-added player")
		}
	}

	t.PlayerNames = append(t.PlayerNames, playerName)
	t.IndexOfPlayer[playerName] = len(t.PlayerNames) - 1
	t.HandOfPlayer[playerName] = NewEmptyDeck()
	return nil
}

func (t *Table) PlayerIndicesSortedByTurn() []int {
	sortedIndices := make([]int, t.PlayerCount())
	curIndex := t.IndexOfPlayer[t.NextPlayerToDraw]
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
	t.DrawDeck.Pop()
	t.Pile.Push(topCard)

	t.Logger.Printf("Top card: %+v", topCard)

	if topCard.Number == CardReverse {
		t.Direction = -t.Direction
	}

	indexOfNextPlayer := t.GetNextPlayerIndex(t.IndexOfPlayer[t.ShufflerName], 1)
	t.NextPlayerToDraw = t.PlayerNames[indexOfNextPlayer]
}

const (
	EligiblePlayerActionPullFromDeck = iota
	EligiblePlayerActionPullFromPile
	EligiblePlayerActionPlayCardFromHand
)

type EligiblePlayerActionInfo struct {
	Action        int
	EligibleCards Deck
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

func (t *Table) EvalPlayerDecisions(playerName string, decisions []PlayerDecision, transferEventsChan chan<- CardTransferEvent) {
	for _, decision := range decisions {
		t.EvalPlayerDecision(playerName, decision, transferEventsChan)
	}
}

// TODO(@rk): Incomplete. Takes a decision event, "evaluates" the bare minimum,
// i.e. update deck/pile/hand of the decision player and pushes the event to the
// transferEventChan. We need to do the whole "game logic" in this function.
func (t *Table) EvalPlayerDecision(playerName string, decision PlayerDecision, transferEventsChan chan<- CardTransferEvent) PlayerDecision {
	handOfPlayer := t.HandOfPlayer[playerName]

	switch decision.Kind {
	case PlayerDecisionPullFromDeck:
		// TODO(@rk): Related to logic - can't allow player to draw card if there's no card in dec
		topCard := t.DrawDeck.MustTop()
		t.HandOfPlayer[playerName] = handOfPlayer.Push(topCard)
		t.DrawDeck = t.DrawDeck.MustPop()

		transferEventsChan <- CardTransferEvent{
			Source:     CardTransferNodeDeck,
			Sink:       CardTransferNodePlayerHand,
			SinkPlayer: playerName,
			Card:       topCard,
		}

		decision.ResultCard = topCard

	case PlayerDecisionPullFromPile:
		// TODO(@rk): See above
		topCard := t.Pile.MustTop()
		t.HandOfPlayer[playerName] = handOfPlayer.Push(topCard)
		t.Pile = t.Pile.MustPop()

		transferEventsChan <- CardTransferEvent{
			Source:     CardTransferNodePile,
			Sink:       CardTransferNodePlayerHand,
			SinkPlayer: playerName,
			Card:       topCard,
		}

		decision.ResultCard = topCard

	case PlayerDecisionPlayHandCard:
		t.PlayCard(playerName, decision.ResultCard, transferEventsChan)
	}

	// TODO(@rk): This also depends on the logic. Simply moving to next player
	playerIndex := t.PlayerIndexFromName(playerName)
	t.NextPlayerToDraw = t.PlayerNames[t.GetNextPlayerIndex(playerIndex, t.Direction)]

	return decision
}

// TODO(@rk): Incomplete. See EvalPlayerDecisionEvent
func (t *Table) PlayCard(playerName string, cardToPlay Card, transferEventsChan chan<- CardTransferEvent) {
	// Remove card from hand and put it on pile
	hand := t.HandOfPlayer[playerName]
	cardLoc := hand.MustFindCard(cardToPlay)
	hand = append(hand[0:cardLoc], hand[cardLoc+1:]...)
	t.HandOfPlayer[playerName] = hand
	t.Pile.Push(cardToPlay)

	transferEventsChan <- CardTransferEvent{
		Source:       CardTransferNodePlayerHand,
		Sink:         CardTransferNodePile,
		SourcePlayer: playerName,
	}

	// TODO(@rk): Evaluate the played card, emitting more transfer events and deciding NextPlayerToDraw
}
