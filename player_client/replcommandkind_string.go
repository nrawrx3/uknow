// Code generated by "stringer -type=ReplCommandKind"; DO NOT EDIT.

package client

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[CmdNone-0]
	_ = x[CmdAskUserToPlay-1]
	_ = x[CmdDeclareReady-2]
	_ = x[CmdQuit-3]
	_ = x[CmdConnect-4]
	_ = x[CmdTableInfo-5]
	_ = x[CmdShowHand-6]
	_ = x[CmdDropCard-7]
	_ = x[CmdDrawCard-8]
	_ = x[CmdDrawCardFromPile-9]
	_ = x[CmdChallenge-10]
}

const _ReplCommandKind_name = "CmdNoneCmdAskUserToPlayCmdDeclareReadyCmdQuitCmdConnectCmdTableInfoCmdShowHandCmdDropCardCmdDrawCardCmdDrawCardFromPileCmdChallenge"

var _ReplCommandKind_index = [...]uint8{0, 7, 23, 38, 45, 55, 67, 78, 89, 100, 119, 131}

func (i ReplCommandKind) String() string {
	if i < 0 || i >= ReplCommandKind(len(_ReplCommandKind_index)-1) {
		return "ReplCommandKind(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _ReplCommandKind_name[_ReplCommandKind_index[i]:_ReplCommandKind_index[i+1]]
}
