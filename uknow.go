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
	ColorWild   Color = 0
	ColorRed    Color = 1
	ColorGreen  Color = 2
	ColorBlue   Color = 3
	ColorYellow Color = 4
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
		cards = append(cards, Card{Number: NumberWildDrawFour, Color: ColorWild})
		cards = append(cards, Card{Number: NumberWild, Color: ColorWild})
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

type TurnStateTag string

// TODO(@rk): Remove unused tags
const (
	StartOfTurn                        TurnStateTag = "start_of_turn"
	AwaitingPlayOrPass                 TurnStateTag = "awaiting_play_or_pass"
	AwaitingWildCardColorDecision      TurnStateTag = "awaiting_wild_card_color_choice"
	AwaitingWildDraw4CardColorDecision TurnStateTag = "awaiting_wild_draw4_card_color_choice"
	AwaitingWildDraw4ChallengeDecision TurnStateTag = "awaiting_wild_draw_4_challenge_choice"
)

func EligibleCommandsAtState(turnState TurnStateTag) string {
	switch turnState {
	case StartOfTurn:
		return "play a card or pull from deck"
	case AwaitingPlayOrPass:
		return "play a card or pass"
	case AwaitingWildCardColorDecision:
		return "wild_color <color>"
	case AwaitingWildDraw4CardColorDecision:
		return "wild_color <color>"
	case AwaitingWildDraw4ChallengeDecision:
		return "challenge or no_challenge"
	}
	return "unknown turnState"
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
	PlayerOfLastTurn string          `json:"player_of_last_turn"`
	Direction        int             `json:"direction"`
	TurnsCompleted   int             `json:"turns_completed"`
	TurnStateTag
	IsShuffled                  bool   `json:"is_shuffled"`
	RequiredColorOfCurrentTurn  Color  `json:"required_color"`
	RequiredColorOfLastTurn     Color  `json:"required_color_of_last_turn"`
	RequiredNumberOfCurrentTurn Number `json:"required_number_of_current_turn"`
	RequiredNumberOfLastTurn    Number `json:"required_number_of_last_turn"`
	RequiredNumberBeforeWild4   Number `json:"required_number_before_wild_4"`

	// ^ We cannot determine elligible play card color simply from top of
	// discard pile since wild card player enfoces a specific color for next
	// player. This field is to be kept in sync whenever a player discards a
	// card
	WinnerPlayerName string
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
	t.PlayerOfLastTurn = other.PlayerOfLastTurn
	t.Direction = other.Direction
	t.IsShuffled = other.IsShuffled
	t.RequiredColorOfCurrentTurn = other.RequiredColorOfCurrentTurn
	t.RequiredColorOfLastTurn = other.RequiredColorOfLastTurn
	t.RequiredNumberOfCurrentTurn = other.RequiredNumberOfCurrentTurn
	t.RequiredNumberOfLastTurn = other.RequiredNumberOfLastTurn
}

func (t *Table) SetPlayerOfNextTurn(nextPlayer string) {
	t.PlayerOfLastTurn = t.PlayerOfNextTurn
	t.PlayerOfNextTurn = nextPlayer
}

func (t *Table) SetRequiredColor(newColor Color) {
	t.RequiredColorOfLastTurn = t.RequiredColorOfCurrentTurn
	t.RequiredColorOfCurrentTurn = newColor
}

func (t *Table) SetRequiredNumber(newNumber Number) {
	t.RequiredNumberOfLastTurn = t.RequiredNumberOfCurrentTurn
	t.RequiredNumberOfCurrentTurn = newNumber
}

func NewAdminTable(logger *log.Logger) *Table {
	return createNewTable(logger)
}

func (t *Table) Summary() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("DrawDeck count: %d\n", t.DrawDeck.Len()))
	sb.WriteString(fmt.Sprintf("DiscardedPile count: %d\n", t.DiscardedPile.Len()))
	sb.WriteString("Hand counts, Index:\n----------\n")
	for playerName, hand := range t.HandOfPlayer {
		sb.WriteString(fmt.Sprintf("%s: %d, %d\n", playerName, hand.Len(), t.IndexOfPlayer[playerName]))
	}
	sb.WriteString(fmt.Sprintf("Shuffler: %s\n", t.ShufflerName))
	sb.WriteString(fmt.Sprintf("Player of last turn: %s\n", t.PlayerOfLastTurn))
	sb.WriteString(fmt.Sprintf("Player of current turn: %s\n", t.PlayerOfNextTurn))
	sb.WriteString(fmt.Sprintf("Direction: %d\n", t.Direction))
	sb.WriteString(fmt.Sprintf("RequiredColor: %s\n", t.RequiredColorOfCurrentTurn.String()))
	sb.WriteString(fmt.Sprintf("RequiredNumber: %s\n", t.RequiredNumberOfCurrentTurn.String()))
	sb.WriteString(fmt.Sprintf("RequiredColorOfLastTurn: %s\n", t.RequiredColorOfLastTurn.String()))
	sb.WriteString(fmt.Sprintf("RequiredNumberOfLastTurn: %s\n", t.RequiredNumberOfLastTurn.String()))

	sb.WriteString("Discard pile top:\n----------\n")
	for i, count := len(t.DiscardedPile)-1, 0; i >= 0 && count <= 5; i, count = i-1, count+1 {
		sb.WriteString(t.DiscardedPile[i].String())
		sb.WriteRune('\n')
	}
	sb.WriteRune('\n')

	return sb.String()
}

func createNewTable(logger *log.Logger) *Table {
	return &Table{
		DrawDeck:      NewFullDeck(),
		DiscardedPile: NewEmptyDeck(),
		HandOfPlayer:  make(map[string]Deck),
		IndexOfPlayer: make(map[string]int),
		PlayerNames:   make([]string, 0, 16),
		Direction:     1,
		Logger:        logger,
		TurnStateTag:  StartOfTurn,
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

func (t *Table) GetPreviousPlayer() (string, error) {
	curPlayerIndex, ok := t.IndexOfPlayer[t.PlayerOfNextTurn]
	if !ok {
		return "", fmt.Errorf("%w: failed to look up index of player: '%s'", ErrShouldNotHappen, t.PlayerOfNextTurn)
	}

	prevPlayerIndex := t.getPlayerIndex(curPlayerIndex, 1, -t.Direction)
	return t.PlayerNames[prevPlayerIndex], nil
}

func (t *Table) getPlayerIndex(startingPlayerIndex int, step int, direction int) int {
	i := (startingPlayerIndex + direction*step) % t.PlayerCount()
	if i < 0 {
		return t.PlayerCount() + i
	}
	return i
}

func (t *Table) GetNextPlayer(step int) (string, error) {
	curPlayerIndex, ok := t.IndexOfPlayer[t.PlayerOfNextTurn]
	if !ok {
		return "", fmt.Errorf("%w: failed to look up index of player: '%s'", ErrShouldNotHappen, t.PlayerOfNextTurn)
	}

	nextPlayerIndex := (curPlayerIndex + t.Direction*step) % t.PlayerCount()
	if nextPlayerIndex < 0 {
		nextPlayerIndex = t.PlayerCount() + nextPlayerIndex
	}

	return t.PlayerNames[nextPlayerIndex], nil
}

func (t *Table) PlayerIndexFromName(playerName string) int {
	for i, name := range t.PlayerNames {
		if name == playerName {
			return i
		}
	}

	panic(fmt.Errorf("non-existent player name: '%s'", playerName))
}

func (t *Table) SetIndexOfPlayer(indexOfPlayer map[string]int) error {
	for playerName, index := range indexOfPlayer {
		_, exists := t.IndexOfPlayer[playerName]
		if !exists {
			return fmt.Errorf("player %s does not exist in table", playerName)
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
		t.SetRequiredColor(ColorRed)
	} else {
		t.SetRequiredColor(topCard.Color)
		t.RequiredColorOfCurrentTurn = topCard.Color
	}

	indexOfNextPlayer := t.GetNextPlayerIndex(t.IndexOfPlayer[t.ShufflerName], 1)
	t.SetPlayerOfNextTurn(t.PlayerNames[indexOfNextPlayer])

	// avoid keeping this as "", since shuffler was the indeed last player
	t.PlayerOfLastTurn = t.ShufflerName

	t.IsShuffled = true
}

type PlayerDecisionKind int

//go:generate stringer -type=PlayerDecisionKind
const (
	PlayerDecisionPullFromDeck PlayerDecisionKind = iota + 1
	// PlayerDecisionPullFromPile
	PlayerDecisionPlayHandCard
	PlayerDecisionPass
	PlayerDecisionWildCardChooseColor
	PlayerDecisionDoChallenge
	PlayerDecisionDontChallenge
)

type PlayerDecision struct {
	Kind                PlayerDecisionKind
	ResultCard          Card  // Only required when Kind == PlayerDecisionPlayHandCard
	WildCardChosenColor Color // Only required when Kind == PlayerDecisionPlayHandCard and ResultCard.Color = Wild
}

func (e *PlayerDecision) IsWildDraw4() bool {
	return e.ResultCard.Number == NumberWildDrawFour
}

func (e *PlayerDecision) String() string {
	resultCard := ""
	if e.Kind == PlayerDecisionPlayHandCard {
		resultCard = ": " + e.ResultCard.String()
	}
	return fmt.Sprintf("%s%s", e.Kind.String(), resultCard)
}

func (t *Table) EvalPlayerDecisionsNoTransferChan(decidingPlayer string, decisions []PlayerDecision) {
	dummyTransferChan := make(chan GameEvent)
	go func() {
		for range dummyTransferChan {
		}
	}()
	t.EvalPlayerDecisions(decidingPlayer, decisions, dummyTransferChan)
}

func (t *Table) EvalPlayerDecisions(decidingPlayer string, decisions []PlayerDecision, gameEventPushChan chan<- GameEvent) {
	for _, decision := range decisions {
		t.EvalPlayerDecision(decidingPlayer, decision, gameEventPushChan)
	}
}

type EvalDecisionError struct {
	Decision PlayerDecision
	Reason   error
}

func (e *EvalDecisionError) Error() string {
	return fmt.Sprintf("%s: %s", e.Reason.Error(), e.Decision.String())
}

func (e *EvalDecisionError) Unwrap() error {
	return e.Reason
}

type IllegalPlayError struct {
	Card           Card
	ExpectedColor  Color
	ExpectedNumber Number
}

func (e *IllegalPlayError) Error() string {
	return fmt.Sprintf("Illegal play of card %s, expected color: %s, or expected number: %s", e.Card.String(), e.ExpectedColor.String(), e.ExpectedNumber.String())
}

var ErrShouldNotHappen = errors.New("something like this should not happen, please check the code")

var ErrCardNotInHand = errors.New("card not in hand")
var ErrAlreadyDrewCard = errors.New("already drew card this turn")
var ErrPassWithoutDrawOrDrop = errors.New("cannot pass without drawing a card or playing a card from hand")
var ErrDrawDeckIsEmpty = errors.New("draw-deck is empty")
var ErrDiscardPileIsEmpty = errors.New("discard pile is empty")
var ErrUnknownPlayer = errors.New("unknown player")
var ErrInvalidDecision = errors.New("invalid decision")
var ErrIllegalPlayCard = errors.New("card illegal")
var ErrUnexpectedDecision = errors.New("unexpected decision")

func (t *Table) NeedMoreUserDecisionToFinishTurn() bool {
	res := t.TurnStateTag == AwaitingWildCardColorDecision ||
		t.TurnStateTag == AwaitingWildDraw4CardColorDecision ||
		t.TurnStateTag == AwaitingPlayOrPass
	t.Logger.Printf("Need more decision from %s? %v", t.LocalPlayerName, res)
	return res
}

// TODO(@rk): Incomplete. Takes a decision event, "evaluates" the bare minimum,
// i.e. update deck/pile/hand of the decision player and pushes the event to the
// transferEventChan. We need to do the whole "game logic" in this function.
// LIST THE NEXT ELIGIBLE CARDS THAT CAN BE PLAYED BY THE PLAYER
func (t *Table) EvalPlayerDecision(decidingPlayer string, decision PlayerDecision, gameEventPushChan chan<- GameEvent) (PlayerDecision, error) {
	switch decision.Kind {
	case PlayerDecisionPullFromDeck:
		if t.TurnStateTag != StartOfTurn {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrAlreadyDrewCard}
		}

		topCard, err := t.PullCardFromDeckToPlayerHand(decidingPlayer, gameEventPushChan)
		if err != nil {
			return decision, &EvalDecisionError{Decision: decision, Reason: err}
		}

		decision.ResultCard = topCard
		t.TurnStateTag = AwaitingPlayOrPass

		gameEventPushChan <- AwaitingPlayOrPassEvent{
			Player:                     decidingPlayer,
			AskDecisionFromLocalPlayer: decidingPlayer == t.LocalPlayerName,
		}

	case PlayerDecisionPass:
		if t.TurnStateTag != AwaitingPlayOrPass {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrPassWithoutDrawOrDrop}
		}

		t.SetNeighborAsNextPlayer(decidingPlayer, StartOfTurn)

	case PlayerDecisionPlayHandCard:
		if t.TurnStateTag != StartOfTurn && t.TurnStateTag != AwaitingPlayOrPass {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrInvalidDecision}
		}

		return t.TryPlayCard(decidingPlayer, decision.ResultCard, gameEventPushChan)

	case PlayerDecisionWildCardChooseColor:
		if t.TurnStateTag != AwaitingWildCardColorDecision && t.TurnStateTag != AwaitingWildDraw4CardColorDecision {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrUnexpectedDecision}
		}

		t.SetRequiredColor(decision.WildCardChosenColor)

		t.Logger.Printf("Setting required color to wild card chosen color %s, previous color: %s", decision.WildCardChosenColor.String(), t.RequiredColorOfLastTurn.String())

		// Two distinct cases for wild and wild_draw_4
		if t.TurnStateTag == AwaitingWildCardColorDecision {
			t.TurnStateTag = StartOfTurn
			t.SetNeighborAsNextPlayer(decidingPlayer, StartOfTurn)
		} else {
			t.TurnStateTag = AwaitingWildDraw4ChallengeDecision
			t.SetNeighborAsNextPlayer(decidingPlayer, AwaitingWildDraw4ChallengeDecision)
		}

	case PlayerDecisionDoChallenge:
		if t.TurnStateTag != AwaitingWildDraw4ChallengeDecision {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrUnexpectedDecision}
		}

		hand := t.HandOfPlayer[t.PlayerOfLastTurn]
		eligibleCards := NewEmptyDeck()

		for _, card := range hand {
			if !card.IsWild() && (card.Color == t.RequiredColorOfLastTurn || card.Number == t.RequiredNumberBeforeWild4) {
				eligibleCards = append(eligibleCards, card)
			}
		}

		t.Logger.Printf(`%s was challenged by %s found eligible cards.
		Last turn's required color: %s,
		Last turn's required number (before wild4 was played): %s,
		Eligible cards found: %+v`, t.PlayerOfLastTurn, t.PlayerOfNextTurn, t.RequiredColorOfLastTurn.String(), t.RequiredNumberBeforeWild4.String(), eligibleCards)

		if eligibleCards.Len() != 0 {
			gameEventPushChan <- ChallengerSuccessEvent{
				ChallengerName:      decidingPlayer,
				WildDraw4PlayerName: t.PlayerOfLastTurn,
				EligibleCards:       eligibleCards,
			}

			for i := 0; i < 4; i++ {
				_, err := t.PullCardFromDeckToPlayerHand(t.PlayerOfLastTurn, gameEventPushChan)
				if err != nil {
					return decision, err
				}
			}
		} else {
			gameEventPushChan <- ChallengerFailedEvent{
				ChallengerName:      decidingPlayer,
				WildDraw4PlayerName: t.PlayerOfLastTurn,
			}

			for i := 0; i < 6; i++ {
				_, err := t.PullCardFromDeckToPlayerHand(decidingPlayer, gameEventPushChan)
				if err != nil {
					return decision, err
				}
			}
		}

		t.SetNeighborAsNextPlayer(decidingPlayer, StartOfTurn)

	case PlayerDecisionDontChallenge:
		if t.TurnStateTag != AwaitingWildDraw4ChallengeDecision {
			return decision, &EvalDecisionError{Decision: decision, Reason: ErrUnexpectedDecision}
		}

		t.SetNeighborAsNextPlayer(decidingPlayer, StartOfTurn)
	}

	t.Logger.Printf("Evaluated decision: %s\n\tfrom player: %s,\n\tplayer of next turn: %s", decision.String(), decidingPlayer, t.PlayerOfNextTurn)

	return decision, nil
}

func (t *Table) SetNeighborAsNextPlayer(currentPlayer string, nextState TurnStateTag) {
	playerIndex := t.PlayerIndexFromName(currentPlayer)
	nextPlayerIndex := t.GetNextPlayerIndex(playerIndex, t.Direction)
	t.SetPlayerOfNextTurn(t.PlayerNames[nextPlayerIndex])
	t.TurnStateTag = nextState

	t.Logger.Printf("Setting next player: %s, current player: %s", t.PlayerOfNextTurn, currentPlayer)
}

// TODO(@rk): Evaluate the played card, emitting more transfer events and deciding NextPlayerToDraw

// CONSIDER(@rk): For replay events, we shouldn't need to check rules.
func (t *Table) TryPlayCard(decidingPlayer string, cardToPlay Card, gameEventPushChan chan<- GameEvent) (PlayerDecision, error) {
	// This procedure's precondition is that it was indeed the player's turn. Given that, it checks if the play is valid
	decision := PlayerDecision{
		Kind:       PlayerDecisionPlayHandCard,
		ResultCard: cardToPlay,
	}

	// cardToPlay must come from hand
	playerHand, ok := t.HandOfPlayer[decidingPlayer]
	if !ok {
		return decision, &EvalDecisionError{
			Decision: decision,
			Reason:   fmt.Errorf("%w: %s", ErrUnknownPlayer, decidingPlayer),
		}
	}

	_, err := playerHand.FindCard(cardToPlay)
	if err != nil {
		return decision, &EvalDecisionError{
			Decision: decision,
			Reason:   ErrCardNotInHand,
		}
	}

	// Check top of discard pile
	topOfPile, err := t.DiscardedPile.Top()
	if err != nil {
		panic("Should be unreachable. Discard pile always has at least 1 card")
	}

	// numberMatches := topOfPile.Number == cardToPlay.Number || cardToPlay.IsWild()
	numberMatches := cardToPlay.Number == t.RequiredNumberOfCurrentTurn || cardToPlay.IsWild()
	colorMatches := t.RequiredColorOfCurrentTurn == cardToPlay.Color

	if !numberMatches && !colorMatches {
		t.Logger.Printf("CANNOT play card: %s", cardToPlay.String())

		return decision, &EvalDecisionError{
			Decision: decision,
			Reason: &IllegalPlayError{
				Card:           cardToPlay,
				ExpectedColor:  t.RequiredColorOfCurrentTurn,
				ExpectedNumber: topOfPile.Number,
			},
		}
	}

	// Can play card

	// Remove card from hand and put it on pile
	hand := t.HandOfPlayer[decidingPlayer]
	cardLoc := hand.MustFindCard(cardToPlay)
	hand = append(hand[0:cardLoc], hand[cardLoc+1:]...)
	t.HandOfPlayer[decidingPlayer] = hand
	t.DiscardedPile = t.DiscardedPile.Push(cardToPlay)

	gameEventPushChan <- CardTransferEvent{
		Source:       CardTransferNodePlayerHand,
		Sink:         CardTransferNodePile,
		SourcePlayer: decidingPlayer,
		Card:         cardToPlay,
	}

	// TODO(@rk): If card player's hand is empty, switch to win state - some ideas around it. Think later.

	if t.HandOfPlayer[decidingPlayer].Len() == 0 {
		t.WinnerPlayerName = decidingPlayer
	}

	if cardToPlay.Number.IsAction() {
		t.EvalPlayedActionCard(decidingPlayer, cardToPlay, gameEventPushChan)
	} else {
		// TODO(@rk): Better to handle in a separate function for all non-action card plays.
		t.SetNeighborAsNextPlayer(decidingPlayer, StartOfTurn)
		t.TurnStateTag = StartOfTurn
		t.SetRequiredColor(cardToPlay.Color)
		t.SetRequiredNumber(cardToPlay.Number)
	}

	return decision, nil
}

func (t *Table) SetNextPlayerSkipOne(decidingPlayer string) (skippedPlayer, nextPlayer string) {
	curPlayerIndex := t.IndexOfPlayer[decidingPlayer]
	skippedPlayerIndex := t.GetNextPlayerIndex(curPlayerIndex, t.Direction)
	skippedPlayer = t.PlayerNames[skippedPlayerIndex]
	nextPlayerIndex := t.GetNextPlayerIndex(skippedPlayerIndex, t.Direction)
	nextPlayer = t.PlayerNames[nextPlayerIndex]
	t.SetPlayerOfNextTurn(nextPlayer)
	return
}

func (t *Table) EvalPlayedActionCard(decidingPlayer string, actionCard Card, gameEventPushChan chan<- GameEvent) {
	switch actionCard.Number {
	case NumberSkip:
		skippedPlayer, nextPlayer := t.SetNextPlayerSkipOne(decidingPlayer)
		t.TurnStateTag = StartOfTurn
		t.SetRequiredColor(actionCard.Color)
		t.SetRequiredNumber(actionCard.Number)

		event := SkipCardActionEvent{
			Player:        decidingPlayer,
			SkippedPlayer: skippedPlayer,
			NextPlayer:    nextPlayer,
		}

		t.Logger.Printf("evaluated skip card action: %s", event.StringMessage(t.LocalPlayerName))

		gameEventPushChan <- event

	case NumberDrawTwo:
		skippedPlayer, nextPlayer := t.SetNextPlayerSkipOne(decidingPlayer)
		t.TurnStateTag = StartOfTurn
		t.SetRequiredColor(actionCard.Color)
		t.SetRequiredNumber(actionCard.Number)

		event := DrawTwoCardActionEvent{
			Player:        decidingPlayer,
			SkippedPlayer: skippedPlayer,
			NextPlayer:    nextPlayer,
		}

		gameEventPushChan <- event

		for i := 0; i < 2; i++ {
			_, err := t.PullCardFromDeckToPlayerHand(skippedPlayer, gameEventPushChan)

			if err != nil {
				t.Logger.Printf("failed to pull card from deck to hand of player %s as part of draw2 action: %v", skippedPlayer, err)
				return
			}
		}

		t.Logger.Printf("evaluated draw2 card action: %s", event.StringMessage(t.LocalPlayerName))

	case NumberReverse:
		curPlayerIndex := t.IndexOfPlayer[decidingPlayer]
		deniedPlayerIndex := t.GetNextPlayerIndex(curPlayerIndex, t.Direction)

		// CONSIDER(@rk): Should reverse card for 2 player game act like
		// skip card instead?
		t.Direction = -1 * t.Direction
		nextPlayerIndex := t.GetNextPlayerIndex(curPlayerIndex, t.Direction)

		t.SetPlayerOfNextTurn(t.PlayerNames[nextPlayerIndex])
		deniedPlayer := t.PlayerNames[deniedPlayerIndex]
		t.TurnStateTag = StartOfTurn
		t.SetRequiredColor(actionCard.Color)
		t.SetRequiredNumber(actionCard.Number)

		event := ReverseCardActionEvent{
			Player:       decidingPlayer,
			DeniedPlayer: deniedPlayer,
			NextPlayer:   t.PlayerOfNextTurn,
		}

		t.Logger.Printf("evaluated reverse card action: %s", event.StringMessage(t.LocalPlayerName))

		gameEventPushChan <- event

	case NumberWild:
		t.TurnStateTag = AwaitingWildCardColorDecision
		t.SetRequiredNumber(NumberWild)
		// NOTE: We don't set required number to wild. Chosen color will be decided by another decision next.

		event := AwaitingWildCardColorDecisionEvent{
			Player:                     decidingPlayer,
			IsDraw4:                    false,
			AskDecisionFromLocalPlayer: decidingPlayer == t.LocalPlayerName,
		}

		gameEventPushChan <- event

	case NumberWildDrawFour:
		t.TurnStateTag = AwaitingWildDraw4CardColorDecision

		// EXPLAIN(@rk): Since we're looking for eligible cards _after_
		// the user challenges, we need to store this. This state needs
		// to be kept _somewhere_, could determine challenge success or
		// not. We could determine it right now and put it into the
		// AwaitingWildCardColorDecisionEvent also, but NOT doing that.
		t.RequiredNumberBeforeWild4 = t.RequiredNumberOfCurrentTurn
		t.SetRequiredNumber(NumberWildDrawFour)
		// NOTE: We don't set required number to wild. Chosen color will be decided by another decision next.

		event := AwaitingWildCardColorDecisionEvent{
			Player:                     decidingPlayer,
			IsDraw4:                    true,
			AskDecisionFromLocalPlayer: decidingPlayer == t.LocalPlayerName,
		}

		gameEventPushChan <- event

	default:
		t.Logger.Panicf("failed to eval action card %s, not implemented", actionCard.String())
	}
}

func (t *Table) ContinueAfterWildCardColorDecision(decidingPlayer string, gameEventPushChan chan<- GameEvent) {
	t.TurnStateTag = StartOfTurn
	curPlayerIndex := t.IndexOfPlayer[decidingPlayer]
	nextPlayerIndex := t.GetNextPlayerIndex(curPlayerIndex, t.Direction)
	nextPlayer := t.PlayerNames[nextPlayerIndex]
	t.SetNeighborAsNextPlayer(nextPlayer, StartOfTurn)
}

// Pull top card from draw deck and put it in target player's hand. Returns the card pulled.
func (t *Table) PullCardFromDeckToPlayerHand(targetPlayer string, gameEventPushChan chan<- GameEvent) (Card, error) {
	topCard, err := t.DrawDeck.Top()
	if err != nil {
		return topCard, ErrDrawDeckIsEmpty
	}

	t.HandOfPlayer[targetPlayer] = t.HandOfPlayer[targetPlayer].Push(topCard)
	t.DrawDeck = t.DrawDeck.MustPop()

	gameEventPushChan <- CardTransferEvent{
		Source:     CardTransferNodeDeck,
		Sink:       CardTransferNodePlayerHand,
		SinkPlayer: targetPlayer,
		Card:       topCard,
	}
	return topCard, nil
}
