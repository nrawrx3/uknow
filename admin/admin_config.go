package admin

type AdminUserConfig struct {
	Type                        string                 `json:"type"` // should always be "admin"
	ListenPort                  int                    `json:"listen_port"`
	ListenIP                    string                 `json:"listen_ip"`
	RunREPL                     bool                   `json:"run_repl"`
	ReadyPlayerName             string                 `json:"ready_player_name"`
	PauseMsecsBeforeNewTurn     int                    `json:"pause_msecs_before_new_turn"`
	AESKeyString                string                 `json:"aes_key"`
	EncryptMessages             bool                   `json:"encrypt_messages"`
	DebugStartingHandConfigFile string                 `json:"debug_starting_hand_config_file"`
	DebugStartingHandConfig     map[string]interface{} `json:"debug_starting_hand_config,omitempty"`
	DebugSignalNewTurnViaPrompt bool                   `json:"debug_signal_new_turn_via_prompt"`
}
