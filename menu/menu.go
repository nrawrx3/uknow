package menu

import (
	"log"

	ui "github.com/gizak/termui/v3"
)

type MenuScreen struct {
	LocalPlayerName string
}

func InitMenuScreen(initUILibrary bool) (*MenuScreen, error) {
	if initUILibrary {
		err := ui.Init()
		if err != nil {
			log.Fatalf("failed to create UI: %v", err)
			return nil, err
		}
	}
}

func (m *MenuScreen) initWidgets() {
}

func (m *MenuScreen) Show() {
}

func (m *MenuScreen) Clear() {
}
