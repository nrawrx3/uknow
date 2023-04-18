package client

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"text/scanner"

	"github.com/nrawrx3/uknow"
)

const (
	ReservedNameDeck          = "<deck>"
	ReservedNamePile          = "<pile>"
	ReservedNameAdmin         = "<admin>"
	ReservedNameClient        = "<player_client>"
	ReservedNameCommandPrompt = "<command_prompt>"
)

// `ReplCommandKind` represents a client command. Both UI and Admin can send these commands to the PlayerClient to do an action.
//
//go:generate stringer -type=ReplCommandKind
type ReplCommandKind int

const (
	CmdNone          ReplCommandKind = iota
	CmdAskUserToPlay                 // Called by server to tell a user it's their turn
	CmdDeclareReady                  // A single user calls this on the server to signal the clients that all players have joined, the server then calls this on each client

	// Non-decision commands
	CmdQuit
	CmdConnect
	CmdTableSummary
	CmdDumpDrawDeck
	CmdShowHand // Might delete since we want to show hand at all times in the UI in the MVP

	// Player decision commands. Add new decision commands to the _middle_ of the list, or update IsUserDecisionCommand function.
	CmdDropCard
	CmdDrawCard
	CmdPass
	CmdDrawCardFromPile // TODO(@rk): Delete this? Not needed.
	CmdSetWildCardColor
	CmdNoChallenge
	CmdChallenge
)

func (k ReplCommandKind) IsUserDecisionCommand() bool {
	return CmdDropCard <= k && k <= CmdChallenge
}

// Represents a single command. Not all fields are used for all commands. TODO(@rk): _maybe_ use a sum type instead
// of clubbing all possible payload in a single struct?
type ReplCommand struct {
	Cards            []uknow.Card    `json:"cards"`
	Kind             ReplCommandKind `json:"kind"`
	Count            int             `json:"count"`
	PlayerName       string          `json:"player_name"`
	TargetPlayerName string          `json:"target_player_name"` // Valid for certain contexts
	ExtraData        interface{}     `json:"extra_data"`
}

func NewReplCommand(kind ReplCommandKind, playerName string) *ReplCommand {
	return &ReplCommand{
		Kind:       kind,
		Cards:      make([]uknow.Card, 0, 1),
		PlayerName: playerName,
	}
}

// Syntax:
//	connect REMOTE_ADDRESS
//	draw NUMBER              (where NUMBER denotes the count of cards to pull)
//	drawpile
//	drop NUMBER COLOR (NUMBER COLOR)*        (where NUMBER can denote or action name or action name)
//	quit                     (quit the game??)
//	challenge NAME           (where NAME is name of player whom to challenge)
//	table_info

func ParseCommandFromInput(input string, playerName string) (*ReplCommand, error) {
	input = strings.TrimSpace(input)

	var s scanner.Scanner
	s.Init(strings.NewReader(input))
	s.Filename = "cmd"
	s.Mode = scanner.GoTokens

	tok := s.Scan()

	if tok == scanner.Ident && s.TokenText() == "connect" {
		return parseConnectCommand(input, playerName)
	}

	tok, command, err := parseCommand(&s, tok, playerName)
	if err != nil {
		return command, err
	}

	if tok != scanner.EOF {
		return command, errors.New("expected EOF during parsing command")
	}

	// log.Printf("Parsed command: %s", command.Kind)

	return command, nil
}

func parseCommand(s *scanner.Scanner, tok rune, playerName string) (rune, *ReplCommand, error) {
	command := NewReplCommand(CmdNone, playerName)

	if tok != scanner.Ident {
		log.Printf("tok = %d, scanner.Ident = %d", tok, scanner.Ident)
		return tok, command, fmt.Errorf("expected a main-command (draw|drop|quit|challenge), found: '%s'", s.TokenText())
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
			return tok, command, fmt.Errorf("expected empty or a number of cards to draw from deck. Got token '%s'", s.TokenText())
		}
		number, err := strconv.Atoi(s.TokenText())
		if err != nil || number > 9 || number < 0 {
			return tok, command, fmt.Errorf("expected a valid number of cards to pull. Got token: %s", s.TokenText())
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
			return tok, command, fmt.Errorf("expected a number (0-9) or action name (skip|rev|draw2|wild|wild4), found: '%s'", s.TokenText())
		}

		cards := command.Cards
		tok, cards, err := parseCardSequence(s, cards)
		if err != nil {
			return tok, command, err
		}
		command.Cards = cards
		return tok, command, nil

	case "wild_color":
		command.Kind = CmdSetWildCardColor
		tok := s.Scan()
		if tok != scanner.Ident {
			return tok, command, fmt.Errorf("expected a color (red|blue|yellow|green) as argument of wild_color command")
		}

		color, ok := getColorFromString(s.TokenText())
		if !ok {
			return tok, command, fmt.Errorf("invalid color: %s", s.TokenText())
		}
		command.ExtraData = color
		return s.Scan(), command, nil

	case "quit":
		command.Kind = CmdQuit
		return s.Scan(), command, nil

	case "challenge":
		command.Kind = CmdChallenge
		return s.Scan(), command, nil

	case "no_challenge":
		command.Kind = CmdNoChallenge
		return s.Scan(), command, nil

	case "pass":
		command.Kind = CmdPass
		return s.Scan(), command, nil

	case "connect_default":
		command.Kind = CmdConnect
		command.ExtraData = nil
		return s.Scan(), command, nil

	case "conndef":
		command.Kind = CmdConnect
		command.ExtraData = nil
		return s.Scan(), command, nil

	case "table_summary":
		command.Kind = CmdTableSummary
		return s.Scan(), command, nil

	case "dump_drawdeck":
		command.Kind = CmdDumpDrawDeck
		countToken := s.Scan()
		if countToken != scanner.Int {
			return countToken, command, fmt.Errorf("missing count in command: dump_drawdeck <count>, have: %s", scanner.TokenString(countToken))
		}
		var err error
		command.Count, err = strconv.Atoi(s.TokenText())
		if err != nil {
			return countToken, command, fmt.Errorf("%w: Failed to parse count in command:dump_drawdeck <count>", err)
		}

		return s.Scan(), command, nil

	case "show_hand":
		command.Kind = CmdShowHand
		return s.Scan(), command, nil

	default:
		return tok, command, fmt.Errorf("expected a main-command (draw|drop|quit|challenge), found '%s'", s.TokenText())
	}
}

func getColorFromString(s string) (uknow.Color, bool) {
	switch strings.ToLower(s) {
	case "red":
		return uknow.ColorRed, true
	case "blue":
		return uknow.ColorBlue, true
	case "green":
		return uknow.ColorGreen, true
	case "yellow":
		return uknow.ColorYellow, true
	default:
		return uknow.ColorWild, false
	}
}

func parseCardSequence(s *scanner.Scanner, cards []uknow.Card) (rune, []uknow.Card, error) {
	card := uknow.Card{}

	switch text := strings.ToLower(s.TokenText()); text {
	case "skip":
		card.Number = uknow.NumberSkip
	case "rev":
		card.Number = uknow.NumberReverse
	case "draw2":
		card.Number = uknow.NumberDrawTwo
	case "wild":
		card.Number = uknow.NumberWild
	case "wild4":
		card.Number = uknow.NumberWildDrawFour
	default:
		number, err := strconv.Atoi(text)
		if err != nil {
			return 0, cards, err
		}
		if number < 0 || number > 9 {
			return 0, cards, fmt.Errorf("invalid card number: %d", number)
		}
		card.Number, err = uknow.IntToNumber(number)
		if err != nil {
			return 0, cards, err
		}
	}

	if card.Number == uknow.NumberWild || card.Number == uknow.NumberWildDrawFour {
		cards = append(cards, card)
		tok := s.Scan()

		if tok == scanner.Ident || tok == scanner.Int {
			return parseCardSequence(s, cards)
		}
		return tok, cards, nil
	}

	tok := s.Scan()
	if tok != scanner.Ident {
		return tok, cards, fmt.Errorf("expected a card color (red|green|blue|yellow). Got '%s'", s.TokenText())
	}
	switch strings.ToLower(s.TokenText()) {
	case "red":
		card.Color = uknow.ColorRed
	case "green":
		card.Color = uknow.ColorGreen
	case "blue":
		card.Color = uknow.ColorBlue
	case "yellow":
		card.Color = uknow.ColorYellow
	default:
		return tok, cards, fmt.Errorf("expected a card color (red|green|blue|yellow). Got '%s'", s.TokenText())
	}
	cards = append(cards, card)

	tok = s.Scan()
	if tok == scanner.Ident || tok == scanner.Int {
		return parseCardSequence(s, cards)
	}
	return tok, cards, nil
}

// Connect command is of the form: connect adminAddr
func parseConnectCommand(input string, playerName string) (*ReplCommand, error) {
	re := regexp.MustCompile(`^connect\\s+(?P<adminAddr>.+)$`)
	input = strings.TrimSpace(input)

	matches := re.FindStringSubmatch(input)
	if matches == nil {
		return &ReplCommand{}, errors.New("expected a `connect <address>` command")
	}
	adminAddrIndex := re.SubexpIndex("adminAddr")
	adminAddr := matches[adminAddrIndex]

	if !strings.HasPrefix(adminAddr, "http://") {
		adminAddr = "http://" + adminAddr
	}

	cmd := NewReplCommand(CmdConnect, playerName)
	cmd.ExtraData = adminAddr
	return cmd, nil
}

func IsUserNameAllowed(name string) bool {
	re := regexp.MustCompile(`^([[:alpha:]]|_)+$`)
	return re.MatchString(name)
}
