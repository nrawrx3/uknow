package uknow

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/scanner"
)

//go:generate stringer -type=CommandKind
type CommandKind int

const (
	ReservedNameDeck          = "<deck>"
	ReservedNamePile          = "<pile>"
	ReservedNameAdmin         = "<admin>"
	ReservedNameClient        = "<player_client>"
	ReservedNameCommandPrompt = "<command_prompt>"
)

const (
	CmdNone          CommandKind = iota
	CmdAskUserToPlay             // Called by server to tell a user it's their turn
	CmdDeclareReady              // A single user calls this on the server to signal the clients that all players have joined, the server then calls this on each client

	// User decision commands. User decision commands are not sent from client to admin or vice versa.
	// They are translated to state modifier commands.
	CmdDropCard
	CmdDrawCard
	CmdDrawCardFromPile
	CmdChallenge

	// User non-game commands
	CmdQuit
	CmdConnect
	CmdTableInfo

	// State modifier commands. These are sent from client to admin and vice versa over the wire.
	CmdAddCards
	CmdMoveCards
	CmdSetHand
	CmdApprove // Only sent by admin to client

	// Add player - admin asks players to add these to their local table
	CmdAddPlayer
)

func (k CommandKind) IsUserDecisionCommand() bool {
	return CmdDropCard <= k && k < CmdQuit
}

// Represents a single command. Not all fields are used for all commands. TODO: _maybe_ use a sum type instead
// of clubbing all possible payload in a single struct?
type Command struct {
	Cards            []Card      `json:"cards"`
	Kind             CommandKind `json:"kind"`
	Count            int         `json:"count"`
	TargetPlayerName string      `json:"target_player_name"`
	ExtraData        interface{} `json:"extra_data"`
}

func NewCommand(kind CommandKind) Command {
	return Command{
		Kind:  kind,
		Cards: make([]Card, 0, 1),
	}
}

func MakeCmdApprove(cmdKindToApprove CommandKind) Command {
	cmd := NewCommand(CmdApprove)
	cmd.ExtraData = int(cmdKindToApprove)
	return cmd
}

func CmdApprovesCommand(cmdApproval, cmdAwaitingApproval Command) bool {
	kindToApprove, ok := cmdApproval.ExtraData.(int)
	if !ok {
		Logger.Fatalf("Unexpected ExtraData in cmdApproval")
	}
	return kindToApprove == int(cmdAwaitingApproval.Kind)
}

// Syntax:
//	connect REMOTE_ADDRESS
//	draw NUMBER              (where NUMBER denotes the count of cards to pull)
//	drawpile
//	drop NUMBER COLOR (NUMBER COLOR)*        (where NUMBER can denote or action name or action name)
//	quit                     (quit the game??)
//	challenge NAME           (where NAME is name of player whom to challenge)
//	table_info

func ParseCommandFromInput(input string) (Command, error) {
	input = strings.TrimSpace(input)

	var s scanner.Scanner
	s.Init(strings.NewReader(input))
	s.Filename = "cmd"
	s.Mode = scanner.GoTokens

	tok := s.Scan()

	if tok == scanner.Ident && s.TokenText() == "connect" {
		return parseConnectCommand(input)
	}

	tok, command, err := parseCommand(&s, tok)
	if err != nil {
		return command, err
	}

	if tok != scanner.EOF {
		return command, errors.New("Expected EOF during parsing command")
	}

	Logger.Printf("Parsed command: %s", command.Kind)

	return command, nil
}

func parseCommand(s *scanner.Scanner, tok rune) (rune, Command, error) {
	command := NewCommand(CmdNone)

	if tok != scanner.Ident {
		Logger.Printf("tok = %d, scanner.Ident = %d", tok, scanner.Ident)
		return tok, command, fmt.Errorf("Expected a main-command (draw|drop|quit|challenge), found: '%s'", s.TokenText())
	}

	switch strings.ToLower(s.TokenText()) {
	case "ready":
		command.Kind = CmdDeclareReady
		return s.Scan(), command, nil

	case "draw":
		command.Kind = CmdDrawCard
		command.Count = 1
		tok := s.Scan()
		if tok == scanner.EOF {
			return tok, command, nil
		}
		if tok != scanner.Int {
			return tok, command, fmt.Errorf("Expected empty or a number of cards to draw from deck. Got token '%s'", s.TokenText())
		}
		number, err := strconv.Atoi(s.TokenText())
		if err != nil || number > 9 || number < 0 {
			return tok, command, fmt.Errorf("Expected a valid number of cards to pull. Got token: %s", s.TokenText())
		}
		command.Count = number
		return tok, command, nil

	case "drawpile":
		command.Kind = CmdDrawCardFromPile
		return s.Scan(), command, nil

	case "drop":
		command.Kind = CmdDropCard
		tok := s.Scan()
		if !(tok == scanner.Int || tok == scanner.Ident) {
			return tok, command, fmt.Errorf("Expected a number (0-9) or action name (skip|rev|draw2|wild|wild4), found: '%s'", s.TokenText())
		}

		cards := command.Cards
		tok, cards, err := parseCardSequence(s, cards)
		if err != nil {
			return tok, command, err
		}
		command.Cards = cards
		return tok, command, nil

	case "quit":
		command.Kind = CmdQuit
		return s.Scan(), command, nil

	case "challenge":
		command.Kind = CmdChallenge
		tok = s.Scan()
		if tok != scanner.Ident {
			return tok, command, fmt.Errorf("Expected name of person to challenge")
		}
		command.TargetPlayerName = s.TokenText()
		return s.Scan(), command, nil

	case "table_info":
		command.Kind = CmdTableInfo
		return s.Scan(), command, nil

	default:
		return tok, command, fmt.Errorf("Expected a main-command (draw|drop|quit|challenge), found '%s'", s.TokenText())
	}
}

func parseCardSequence(s *scanner.Scanner, cards []Card) (rune, []Card, error) {
	card := Card{}

	switch text := strings.ToLower(s.TokenText()); text {
	case "skip":
		card.Number = CardSkip
	case "rev":
		card.Number = CardReverse
	case "draw2":
		card.Number = CardDrawTwo
	case "wild":
		card.Number = CardWild
	case "wild4":
		card.Number = CardWildDrawFour
	default:
		number, err := strconv.Atoi(text)
		if err != nil {
			return 0, cards, err
		}
		if number < 0 || number > 9 {
			return 0, cards, fmt.Errorf("Invalid card number: %d", number)
		}
		card.Number, err = IntToNumber(number)
		if err != nil {
			return 0, cards, err
		}
	}

	if card.Number == CardWild || card.Number == CardWildDrawFour {
		cards = append(cards, card)
		tok := s.Scan()

		if tok == scanner.Ident || tok == scanner.Int {
			return parseCardSequence(s, cards)
		}
		return tok, cards, nil
	}

	tok := s.Scan()
	if tok != scanner.Ident {
		return tok, cards, fmt.Errorf("Expected a card color (red|green|blue|yellow). Got '%s'", s.TokenText())
	}
	switch strings.ToLower(s.TokenText()) {
	case "red":
		card.Color = Red
	case "green":
		card.Color = Green
	case "blue":
		card.Color = Blue
	case "yellow":
		card.Color = Yellow
	default:
		return tok, cards, fmt.Errorf("Expected a card color (red|green|blue|yellow). Got '%s'", s.TokenText())
	}
	cards = append(cards, card)

	tok = s.Scan()
	if tok == scanner.Ident || tok == scanner.Int {
		return parseCardSequence(s, cards)
	}
	return tok, cards, nil
}

// Connect command is of the form: connect adminAddr
func parseConnectCommand(input string) (Command, error) {
	re := regexp.MustCompile("^connect\\s+(?P<adminAddr>.+)$")
	input = strings.TrimSpace(input)

	matches := re.FindStringSubmatch(input)
	if matches == nil {
		return Command{}, errors.New("Expected a `connect <address>` command")
	}
	adminAddrIndex := re.SubexpIndex("adminAddr")
	adminAddr := matches[adminAddrIndex]

	if !strings.HasPrefix(adminAddr, "http://") {
		adminAddr = "http://" + adminAddr
	}

	cmd := NewCommand(CmdConnect)
	cmd.ExtraData = adminAddr
	return cmd, nil
}

func IsUserNameAllowed(name string) bool {
	re := regexp.MustCompile(`^([[:alpha:]]|_)+$`)
	return re.MatchString(name)
}
