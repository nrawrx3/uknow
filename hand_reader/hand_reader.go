// Read a JSON representation of table-state and instantiate a `Table` using
// that. This is only for testing/debugging purpose.
package hand_reader

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strings"

	"github.com/rksht/uknow"
)

/*
	{
		"player.alice": {
			"red": [1, 2, "skip"],
			"green": [9, "draw_2"],
			"wilds": ["wild", "wild_draw_4"],

			"draw_upto": {
				"total": 12
			}
		},

		"player.john": {
			"red": [5, 2, "skip"],
			"blue": [7, 9, 2, "reverse"],
			"wild"
		},

		"player.jane": {
			"draw_upto": {
				"total": 8
			}
		},

		"discarded_pile_size": 16,
		"shuffle_seed": 0 // 0 says don't shuffle before distributing,
		"player_of_next_turn": "alice" // which player's turn it is on starting
	}
*/

type drawUpto struct {
	total int
}

type handDesc struct {
	cardsOfColor map[uknow.Color][]uknow.Card
	drawUpto     drawUpto
}

type serializedJSON struct {
	handDescOfPlayer  map[string]*handDesc
	discardedPileSize int
	playerToDraw      string
}

var ErrLocalPlayerNameNotDescribed = errors.New("local player name is not in hand-desc map")
var ErrUnknownKey = errors.New("unknown key")

// Reads the hand-config JSON string and modifies the given table accordingly.
// The table should be the returned value of NewTable(...) or
// NewAdminTable(...), and not modified before passing it to this function.
// TODO(@rk): Add preset discard pile.
func LoadConfig(bytes []byte, initializedTable *uknow.Table, logger *log.Logger) (*uknow.Table, error) {
	var j map[string]interface{}
	err := json.Unmarshal(bytes, &j)
	if err != nil {
		return nil, err
	}

	handDescOf := make(map[string]*handDesc)
	discardedPileSize := 0
	playerOfNextTurn := ""

	for key, value := range j {
		if strings.HasPrefix(key, "player.") {
			playerName := strings.TrimPrefix(key, "player.")
			handDesc, err := castHandDescMap(value)
			if err != nil {
				return nil, fmt.Errorf("ReadJSON: player '%s': %w", playerName, err)
			}

			handDescOf[playerName] = handDesc
		} else if key == "discarded_pile_size" {
			number, ok := value.(float64)
			if !ok {
				return nil, fmt.Errorf("expected an integer value for discard_pile_size")
			}
			discardedPileSize = int(number)
		} else if key == "shuffle_seed" {
			rand.Seed(int64(value.(float64)))
		} else if key == "player_of_next_turn" {
			playerOfNextTurn = strings.TrimSpace(value.(string))
		} else {
			return nil, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
	}

	serializedJSON := serializedJSON{
		handDescOfPlayer:  handDescOf,
		discardedPileSize: discardedPileSize,
		playerToDraw:      playerOfNextTurn,
	}

	return makeTable(serializedJSON, initializedTable, logger)
}

var ErrCouldNotRemoveCard = errors.New("could not remove card")

// updates countOfCard by removing each given card in cards slice
func removeCardsFromDeck(cards []uknow.Card, countOfCard map[uint32]int) error {
	for _, card := range cards {
		enc := card.EncodeUint32()
		count := countOfCard[enc]
		if count == 0 {
			return fmt.Errorf("%w: %s", ErrCouldNotRemoveCard, card.String())
		}
		countOfCard[enc] = count - 1
	}
	return nil
}

func makeTable(serializedJSON serializedJSON, table *uknow.Table, logger *log.Logger) (*uknow.Table, error) {
	countOfCard := make(map[uint32]int)
	for _, card := range table.DrawDeck {
		countOfCard[card.EncodeUint32()] += 1
	}

	for playerName, handDesc := range serializedJSON.handDescOfPlayer {
		err := table.AddPlayer(playerName)
		if err != nil {
			logger.Print(err)
			return nil, err
		}

		for _, cards := range handDesc.cardsOfColor {
			err := removeCardsFromDeck(cards, countOfCard)

			if err != nil {
				return nil, err
			}

			table.HandOfPlayer[playerName] = table.HandOfPlayer[playerName].Push(cards...)
		}
	}

	// create new DrawDeck with the cards removed
	newDrawDeck := uknow.NewEmptyDeck()
	for encodedCard, count := range countOfCard {
		for i := 0; i < count; i++ {
			newDrawDeck = newDrawDeck.Push(uknow.MustDecodeCardFromUint32(encodedCard))
		}
	}
	table.DrawDeck = newDrawDeck

	for i := 0; i < serializedJSON.discardedPileSize; i++ {
		c := table.DrawDeck.MustTop()
		table.DrawDeck = table.DrawDeck.MustPop()
		table.DiscardedPile = table.DiscardedPile.Push(c)
		table.RequiredColor = c.Color

		logger.Printf("hand-reader: updated required color: %s", c.Color.String())
	}

	table.IsShuffled = true
	table.PlayerOfNextTurn = serializedJSON.playerToDraw
	table.PlayerTurnState = uknow.TurnStateStart

	return table, nil
}

func colorFromKey(colorKey string) (uknow.Color, error) {
	switch strings.ToLower(colorKey) {
	case "red":
		return uknow.Red, nil
	case "blue":
		return uknow.Blue, nil
	case "green":
		return uknow.Green, nil
	case "yellow":
		return uknow.Yellow, nil
	case "wild":
		return uknow.Wild, nil
	default:
		return uknow.Wild, fmt.Errorf("unknown color key: '%s'", colorKey)
	}
}

func numberFromSpecial(special string) (uknow.Number, error) {
	switch strings.ToLower(special) {
	case "skip":
		return uknow.NumberSkip, nil
	case "reverse":
		return uknow.NumberReverse, nil
	case "draw_2":
		return uknow.NumberDrawTwo, nil
	case "wild":
		return uknow.NumberWild, nil
	case "wild_draw_4":
		return uknow.NumberWildDrawFour, nil
	default:
		return uknow.Number(0), fmt.Errorf("unknown special number key: %s", special)
	}
}

func tryCastNumber(v interface{}) (uknow.Number, error) {
	number, ok := v.(float64)
	if ok {
		if number != math.Floor(number) {
			return 0, fmt.Errorf("expected integer in place of %f", number)
		}
		return uknow.Number(number), nil
	}

	specialString, ok := v.(string)
	if !ok {
		return uknow.Number(0), errors.New("could to cast value to uknow.Number")
	}
	return numberFromSpecial(specialString)
}

func castHandDescMap(handDescIF interface{}) (*handDesc, error) {
	handDescMap, ok := handDescIF.(map[string]interface{})
	if !ok {
		return nil, errors.New("could not cast handDecMapIF")
	}

	handDesc := &handDesc{
		cardsOfColor: make(map[uknow.Color][]uknow.Card),
		drawUpto:     drawUpto{},
	}

	handDesc.cardsOfColor[uknow.Red] = make([]uknow.Card, 0, len(handDescMap))
	handDesc.cardsOfColor[uknow.Blue] = make([]uknow.Card, 0, len(handDescMap))
	handDesc.cardsOfColor[uknow.Green] = make([]uknow.Card, 0, len(handDescMap))
	handDesc.cardsOfColor[uknow.Yellow] = make([]uknow.Card, 0, len(handDescMap))

	for key, valueIF := range handDescMap {
		if key == "draw_upto" {
			drawUpto, err := castDrawUpto(valueIF)
			if err != nil {
				return nil, err
			}
			handDesc.drawUpto = drawUpto
			continue
		}

		color, err := colorFromKey(key)
		if err != nil {
			return nil, err
		}

		numberList, ok := valueIF.([]interface{})
		if !ok {
			return nil, fmt.Errorf("failed to cast number-list value to array for color %s", key)
		}

		for i, numberIF := range numberList {
			number, err := tryCastNumber(numberIF)
			if err != nil {
				return nil, fmt.Errorf("card index %d: %w", i, err)
			}

			card := uknow.Card{
				Color:  color,
				Number: number,
			}

			handDesc.cardsOfColor[color] = append(handDesc.cardsOfColor[color], card)
		}
	}

	return handDesc, nil
}

func castDrawUpto(v interface{}) (drawUpto, error) {
	drawUpto := drawUpto{}

	object, ok := v.(map[string]interface{})
	if !ok {
		return drawUpto, errors.New("failed to cast drawUpto object")
	}

	for key, value := range object {
		if key == "total" {
			total, ok := value.(float64)
			if !ok {
				return drawUpto, errors.New("failed to cast value of 'total'")
			}
			drawUpto.total = int(total)
		}
	}
	return drawUpto, nil
}
