// Code generated by "stringer -type=CommandKind"; DO NOT EDIT.

package uknow

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[CmdDropCard-1]
	_ = x[CmdDrawCard-2]
	_ = x[CmdDrawCardFromPile-3]
	_ = x[CmdQuit-4]
	_ = x[CmdChallenge-5]
	_ = x[CmdConnect-6]
}

const _CommandKind_name = "CmdDropCardCmdDrawCardCmdDrawCardFromPileCmdQuitCmdChallengeCmdConnect"

var _CommandKind_index = [...]uint8{0, 11, 22, 41, 48, 60, 70}

func (i CommandKind) String() string {
	i -= 1
	if i < 0 || i >= CommandKind(len(_CommandKind_index)-1) {
		return "CommandKind(" + strconv.FormatInt(int64(i+1), 10) + ")"
	}
	return _CommandKind_name[_CommandKind_index[i]:_CommandKind_index[i+1]]
}
