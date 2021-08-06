package uknow

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sort"
)

var Logger = log.Default()

func LogInfo(format string, args ...interface{}) {
	pc := make([]uintptr, 15)
	n := runtime.Callers(2, pc)
	frames := runtime.CallersFrames(pc[:n])
	frame, _ := frames.Next()

	format = fmt.Sprintf("%s:%d:%s> ", frame.File, frame.Line, frame.Function) + format
	Logger.Printf(format, args...)
}

type Card struct {
	Number Number
	Color  Color
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

func (d Deck) Len() int {
	return len(d)
}

func (d Deck) Less(i, j int) bool {
	return d[i].Number < d[j].Number || d[i].Number == d[j].Number && d[i].Color == d[j].Color
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
	for i := 0; i < 4; i++ 
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

func (d Deck) Pop() (Deck, error) {
	if d.IsEmpty() {
		return d, errors.New("Empty deck")
	}
	return d[0 : len(d)-1], nil
}

func (d Deck) RemoveCard(index int) Deck {
	return append(d[0:index], d[index+1:]...)
}

type State int

//go:generate stringer -type=State
const (
	StateBeforeReady State = iota
	StateBeforeShuffle
	StateWaitForDraw
)

type Table struct {
	DrawDeck         Deck
	Pile             Deck
	State            State
	IndexOfPlayer    map[string]int
	HandOfPlayer     map[string]Deck
	PlayerNames      StringSlice
	LocalPlayerName  string
	ShufflerName     string
	NextPlayerToDraw string
	Direction        int
}

func NewTable(localPlayerName string) *Table {
	table := &Table{
		DrawDeck: NewFullDeck(),
		Pile:     NewEmptyDeck(),
		State:    StateBeforeReady,
	}

	table.HandOfPlayer = make(map[string]Deck)
	table.IndexOfPlayer = make(map[string]int)
	table.PlayerNames = make([]string, 0, 16)

	table.LocalPlayerName = localPlayerName
	table.AddPlayer(localPlayerName)

	return table
}

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

// This is one of the only moments where we have to communicate the shuffled indices
func (t *Table) ShuffleDeckAndDistribute(shufflerName string) []int {
	deckSize := len(t.DrawDeck)
	shuffledIndices := ShuffleIntRange(0, deckSize)

	for i, j := range shuffledIndices {
		t.DrawDeck.Swap(i, j)
	}

	topCard, _ := t.DrawDeck.Top()
	t.DrawDeck.Pop()
	t.Pile.Push(topCard)

	if topCard.Number == CardReverse {
		t.Direction = -t.Direction
	}

	indexOfNextPlayer := t.GetNextPlayerIndex(t.IndexOfPlayer[shufflerName], 1)
	t.NextPlayerToDraw = t.PlayerNames[indexOfNextPlayer]
	return shuffledIndices
}

// func (p *Player) PullCard(t *Table) (Card, error) {
// 	if t.DrawDeck.IsEmpty() {
// 		return Card{}, errors.New("DrawDeck empty")
// 	}

// 	topCard, _ := t.DrawDeck.Top()
// 	p.Hand = p.Hand.Push(topCard)
// 	t.DrawDeck, _ = t.DrawDeck.Pop()
// 	return topCard, nil
// }

// func (p *Player) DropCard(cardIndex int, t *Table) (Card, error) {
// 	card := p.Hand[cardIndex]
// 	t.Pile = t.Pile.Push(card)
// 	if cardIndex >= len(p.Hand) {
// 		return Card{}, errors.New(fmt.Sprintf("Invalid card index '%d'", cardIndex))
// 	}
// 	p.Hand = p.Hand.RemoveCard(cardIndex)
// 	return card, nil
// }
