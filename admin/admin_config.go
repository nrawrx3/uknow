package admin

type EnvConfig struct {
	ListenAddr              string `split_words:"true" required:"true"`
	ListenPort              int    `split_words:"true" required:"true"`
	RunREPL                 bool   `split_words:"true" required:"false" default:"true"`
	SetReadyPlayer          string `split_words:"true" required:"true"`
	PauseMsecsBeforeNewTurn int    `split_words:"true" requured:"true"`

	// Testing, debugging related options
	DebugNewTurnViaPrompt       bool   `split_words:"true" default:"false"`
	DebugStartingHandConfigJSON string `split_words:"true"`
}
