package client

import (
	"time"

	"github.com/rksht/uknow"
)

// These are commands that the ClientUI _receives_ from the PlayerClient.

type UICommand interface {
	uiCommandDummy()
}

type UICommandAddPlayer struct {
	PlayerName string
	HandCount  int
}

func (*UICommandAddPlayer) uiCommandDummy() {}

type UICommandAskUserForDecision struct {
	// The PlayerClient itself will wait on this channel to receive the command input from user
	receive              chan<- *ReplCommand
	allowOneMoreDecision <-chan bool
	timeout              time.Duration
	sender               string
}

func (*UICommandAskUserForDecision) uiCommandDummy() {}

type UICommandSetServedCards struct {
	table *uknow.Table
}

func (*UICommandSetServedCards) uiCommandDummy() {}

type UICommandPlayCardTransferEvent struct {
	event *uknow.CardTransferEvent
}

func (*UICommandPlayCardTransferEvent) uiCommandDummy() {}
