package client

import (
	"time"

	"github.com/nrawrx3/uknow"
)

// These are commands that the ClientUI _receives_ from the PlayerClient.

// TODO(@rk): I see only a couple of commands and these are used in different
// codepaths and we also have different channels in player_client and
// player_client_ui for each type. So it's better to not wrap them in an
// interface.
type UICommand interface {
	uiCommandDummy()
}

type AskUserForDecisionResult struct {
	AskForOneMoreDecision bool
	Error                 error
}

type UICommandAskUserForDecision struct {
	// The PlayerClient itself will wait on this channel to receive the command input from user
	receive             chan<- *ReplCommand
	decisionResultChan  <-chan AskUserForDecisionResult
	timeout             time.Duration
	sender              string
	challengeablePlayer string
}

func (d *UICommandAskUserForDecision) LocalPlayerCanChallenge() bool {
	return d.challengeablePlayer != ""
}

func (d *UICommandAskUserForDecision) SetChallengeablePlayer(challengeablePlayer string) {
	d.challengeablePlayer = challengeablePlayer
}

func (*UICommandAskUserForDecision) uiCommandDummy() {}

type UICommandSetServedCards struct {
	table *uknow.Table
}

func (*UICommandSetServedCards) uiCommandDummy() {}
