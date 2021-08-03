package main

import (
	"log"
	"sync"
	"testing"
	"time"
)

// Creates a console without any GUI. Which, I guess, doesn't make it a console anymore
func newTestConsole() *Console {
	c := &Console{}
	c.uiActionCond = sync.NewCond(&c.uiActionMutex)
	c.initWidgetObjects()
	return c
}

func createConsoles(playerNames []string) map[string]*Console {
	consoleOfPlayer := make(map[string]*Console, len(playerNames))
	for i, playerName := range playerNames {
		c := newTestConsole()
		c.createTableAndStartServer(playerName)
		consoleOfPlayer[playerNames[i]] = c
	}
	return consoleOfPlayer
}

func shutdownConsoles(consoleOfPlayer map[string]*Console) {
	for _, c := range consoleOfPlayer {
		c.shutdown()
	}
}

func TestPlayersConnectTransitively(t *testing.T) {
	playerNames := []string{"john", "jane", "jack", "jill"}
	cp := createConsoles(playerNames)

	time.Sleep(time.Duration(100) * time.Millisecond)

	playerAddress := make(map[string]string)
	for _, playerName := range playerNames {
		playerAddress[playerName] = cp[playerName].listener.Addr().String()
	}

	// Connect john and jane, and connect jack and jill.
	cp["john"].stateMutex.Lock()
	cp["john"].connectToPeer(cp["jane"].listener.Addr().String())
	cp["john"].stateMutex.Unlock()

	cp["jack"].stateMutex.Lock()
	cp["jack"].connectToPeer(cp["jill"].listener.Addr().String())
	cp["jack"].stateMutex.Unlock()

	for _, playerName := range playerNames {
		c := cp[playerName]
		c.stateMutex.Lock()
		if count := cp[playerName].table.PlayerCount(); count != 2 {
			t.Errorf("player %s expected to have playerCount() = 2, but got: %d", playerName, count)
		}
		c.stateMutex.Unlock()
	}

	// Connect john and jill, then all 4 should be connected
	cp["john"].stateMutex.Lock()
	cp["john"].connectToPeer(playerAddress["jill"])
	cp["john"].stateMutex.Unlock()

	time.Sleep(time.Duration(1) * time.Second)

	for _, playerName := range playerNames {
		c := cp[playerName]
		c.stateMutex.Lock()
		if count := c.table.PlayerCount(); count != 4 {
			t.Errorf("player %s expected to have playerCount() = 4, but got: %d", playerName, count)
		}
		c.stateMutex.Unlock()
	}

	log.Printf("Done test")
}
