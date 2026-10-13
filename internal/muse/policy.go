package muse

type TimeoutDecision string

const (
	TimeoutDeny  TimeoutDecision = "deny"
	TimeoutAllow TimeoutDecision = "allow"
)

type Policy struct {
	RequireApproval bool            `json:"require_approval"`
	TimeoutSeconds  int             `json:"timeout_seconds"`
	OnTimeout       TimeoutDecision `json:"on_timeout"`
}

func RiskFor(action Action) Risk {
	switch action {
	case ActionSearchFiles, ActionListFiles, ActionReadFile, ActionGitStatus,
		ActionGitDiff, ActionProcessList, ActionRecall:
		return RiskSafe
	case ActionWebSearch, ActionWebFetch, ActionDownload:
		return RiskNetwork
	case ActionWriteFile, ActionPatchFile, ActionMultiEdit, ActionMoveFile,
		ActionRemember, ActionCheckpoint, ActionRestore:
		return RiskMutation
	case ActionDeleteFile:
		return RiskDestructive
	case ActionShell, ActionProcessKill, ActionGitCommit, ActionGitPush,
		ActionGitCheckout, ActionSelfImprove, ActionPromote:
		return RiskCommand
	case ActionConnector, ActionBrowser, ActionEmail:
		return RiskExternal
	case ActionPayment:
		return RiskPayment
	default:
		return RiskUnknown
	}
}

func PolicyFor(action Action) Policy {
	switch action {
	case ActionSearchFiles, ActionListFiles, ActionReadFile, ActionGitStatus,
		ActionGitDiff, ActionProcessList, ActionRecall, ActionWebSearch, ActionWebFetch:
		return Policy{}
	case ActionDownload:
		// Owner preference: only download may auto-allow after five seconds.
		return Policy{RequireApproval: true, TimeoutSeconds: 5, OnTimeout: TimeoutAllow}
	default:
		// Mutations, destructive actions, commands, git writes, browser/app
		// actions, email, payment, promotion and unknown actions all fail closed.
		return Policy{RequireApproval: true, TimeoutSeconds: 0, OnTimeout: TimeoutDeny}
	}
}
