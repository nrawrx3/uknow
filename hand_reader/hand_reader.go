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
		"preset_discard_pile_top": [ // top to bottom
			["red", 9],
			["red", "skip"],
			["wild_draw_4"]
		]
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
	handDescOfPlayer     map[string]*handDesc
	discardedPileSize    int
	playerToDraw         string
	presetDiscardPileTop uknow.Deck
}

var ErrLocalPlayerNameNotDescribed = errors.New("local player name is not in hand-desc map")
var ErrUnknownKey = errors.New("unknown key")
var ErrUnexpectedJSONType = errors.New("unexpected JSON type")

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
	presetDiscardPileTop := make(uknow.Deck, 0)

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
		} else if key == "preset_discard_pile_top" {
			presetDiscardPileTop, err = parsePresetDiscardPileTop(value)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
	}

	serializedJSON := serializedJSON{
		handDescOfPlayer:     handDescOf,
		discardedPileSize:    discardedPileSize,
		playerToDraw:         playerOfNextTurn,
		presetDiscardPileTop: presetDiscardPileTop,
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

	// Create the preset discard pile first
	err := removeCardsFromDeck(serializedJSON.presetDiscardPileTop, countOfCard)
	if err != nil {
		return table, fmt.Errorf("%w: invalid card setup in preset_discard_pile_top", err)
	}
	table.DiscardedPile = table.DiscardedPile.Push(serializedJSON.presetDiscardPileTop...)

	// Assign cards to each player hand from the remaining cards
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

	// create new DrawDeck with the remaining cards.
	newDrawDeck := uknow.NewEmptyDeck()
	for encodedCard, count := range countOfCard {
		for i := 0; i < count; i++ {
			newDrawDeck = newDrawDeck.Push(uknow.MustDecodeCardFromUint32(encodedCard))
		}
	}
	table.DrawDeck = newDrawDeck

	// If discarded pile size is more than the number of cards added by preset discard pile top, add them.
	remainingDiscardPileSize := serializedJSON.discardedPileSize - table.DiscardedPile.Len()
	if remainingDiscardPileSize > 0 {
		remainingDiscardPile := uknow.NewEmptyDeck()
		for i := 0; i < remainingDiscardPileSize; i++ {
			c := table.DrawDeck.MustTop()
			table.DrawDeck = table.DrawDeck.MustPop()
			remainingDiscardPile = remainingDiscardPile.Push(c)
			table.DrawDeck = table.DrawDeck.MustPop()
		}

		table.DiscardedPile = append(remainingDiscardPile, table.DiscardedPile...)
	}

	table.RequiredColor = table.DiscardedPile.MustTop().Color
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

func parsePresetDiscardPileTop(value interface{}) (uknow.Deck, error) {
	array, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w: expected an array for key preset_discard_pile_top", ErrUnexpectedJSONType)
	}

	presetPileTop := make(uknow.Deck, 0, len(array))

	for i, cardDesc := range array {
		tupleCardDesc, ok := cardDesc.([]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: expected an array describing a single card for the preset_discard_pile_top array item at index: %d", ErrUnexpectedJSONType, i)
		}

		var card uknow.Card

		switch len(tupleCardDesc) {
		case 1:
			// Wild card
			wildCardName, ok := tupleCardDesc[0].(string)
			if !ok || (wildCardName != "wild" && wildCardName != "wild_draw_4") {
				return nil, fmt.Errorf("%w: expected either wild or wild_draw_4 string for preset_discard_pile_top array item at index: %d", ErrUnexpectedJSONType, i)
			}
			switch wildCardName {
			case "wild":
				card = uknow.Card{
					Number: uknow.NumberWild,
					Color:  uknow.Wild,
				}
			case "wild_draw_4":
				card = uknow.Card{
					Number: uknow.NumberWildDrawFour,
					Color:  uknow.Wild,
				}
			}

		case 2:
			// Non wild card
			colorName, ok := tupleCardDesc[0].(string)
			if !ok || (colorName != "red" && colorName != "blue" && colorName != "yellow" && colorName != "green") {
				return nil, fmt.Errorf("%w: expected a color name as first array element for preset_discard_pile_top array item at index: %d", ErrUnexpectedJSONType, i)
			}

			switch colorName {
			case "red":
				card.Color = uknow.Red
			case "blue":
				card.Color = uknow.Blue
			case "yellow":
				card.Color = uknow.Yellow
			case "green":
				card.Color = uknow.Green
			}

			switch number := tupleCardDesc[1].(type) {
			case float64:
				if 0 <= number && number <= 9 {
					card.Number = uknow.Number(number)
				} else {
					return nil, fmt.Errorf("invalid number for card description for preset_discard_pile_top array item at index: %d", i)
				}
			case string:
				switch number {
				case "skip":
					card.Number = uknow.NumberSkip
				case "reverse":
					card.Number = uknow.NumberReverse
				case "draw_2":
					card.Number = uknow.NumberDrawTwo
				default:
					return nil, fmt.Errorf("invalid number for card description for preset_discard_pile_top array item at index: %d", i)
				}
			default:
				return nil, fmt.Errorf("%w: expected an integer or a string as card number for preset_discard_pile_top array item at index : %d", ErrUnexpectedJSONType, i)
			}

		default:
			return nil, fmt.Errorf("expected a tuple of size 1 or 2 for describing a card for preset_discard_pile_top array item at index: %d", i)
		}

		presetPileTop = presetPileTop.Push(card)
	}

	// Reverse because the cards are arranged in the array in top to bottom order
	for i, j := 0, len(presetPileTop)-1; i < j; i, j = i+1, j-1 {
		presetPileTop[i], presetPileTop[j] = presetPileTop[j], presetPileTop[i]
	}
	return presetPileTop, nil
}
