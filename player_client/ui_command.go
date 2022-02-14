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

type UICommandAskForUserInput struct {
	// The PlayerClient itself will wait on this channel to receive the command input from user
	receive               chan<- uknow.Command
	timeout               time.Duration
	sender                string
	appQuitting           bool // Set by client to let UI know that the app is stopping
	expectingUserDecision bool // Set by client to make UI expect a user decision command
}

func (*UICommandAskForUserInput) uiCommandDummy() {}

type UICommandSetServedCards struct {
	table *uknow.Table
}

func (*UICommandSetServedCards) uiCommandDummy() {}
