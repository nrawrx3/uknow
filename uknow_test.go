package uknow

import (
	"log"
	"testing"
)

func TestNewDeckHas104Cards(t *testing.T) {
	table := NewTable([]string{"buttstallion", "handsomejack"}, "handsomejack")

	log.Printf("Table created, new deck len = %d", len(table.DrawDeck))

	if table.DrawDeck.Len() != 108 {
		t.Errorf("Length of new deck != 108")
	}
}

func TestCommandParser(t *testing.T) {
	p, err := ParseCommandFromInput("draw")
	if err != nil {
		t.Error(err)
	}

	if p.Kind != CmdDrawCard {
		t.Errorf("%v != %v", p.Kind, CmdDrawCard)
	}
}
