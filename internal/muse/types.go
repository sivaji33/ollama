package muse

import "time"

type Risk string

const (
	RiskSafe        Risk = "safe"
	RiskNetwork     Risk = "network"
	RiskMutation    Risk = "mutation"
	RiskDestructive Risk = "destructive"
	RiskCommand     Risk = "command"
	RiskExternal    Risk = "external"
	RiskPayment     Risk = "payment"
	RiskUnknown     Risk = "unknown"
)

type Action string

const (
	ActionSearchFiles Action = "search_files"
	ActionListFiles   Action = "list_files"
	ActionReadFile    Action = "read_file"
	ActionWriteFile   Action = "write_file"
	ActionPatchFile   Action = "apply_patch"
	ActionMultiEdit   Action = "multi_edit"
	ActionDeleteFile  Action = "delete_file"
	ActionMoveFile    Action = "move_file"
	ActionShell       Action = "shell"
	ActionProcessList Action = "process_list"
	ActionProcessKill Action = "process_kill"
	ActionWebSearch   Action = "web_search"
	ActionWebFetch    Action = "web_fetch"
	ActionDownload    Action = "download"
	ActionGitStatus   Action = "git_status"
	ActionGitDiff     Action = "git_diff"
	ActionGitCommit   Action = "git_commit"
	ActionGitPush     Action = "git_push"
	ActionGitCheckout Action = "git_checkout"
	ActionConnector   Action = "connector_call"
	ActionBrowser     Action = "browser_action"
	ActionRemember    Action = "remember"
	ActionRecall      Action = "recall"
	ActionCheckpoint  Action = "checkpoint"
	ActionRestore     Action = "restore"
	ActionSelfImprove Action = "self_improve"
	ActionPromote     Action = "promote"
	ActionEmail       Action = "email"
	ActionPayment     Action = "payment"
	ActionUnknown     Action = "unknown"
)

type Request struct {
	ID        string         `json:"id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Action    Action         `json:"action"`
	Target    string         `json:"target,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
}

type Result struct {
	OK        bool      `json:"ok"`
	Action    Action    `json:"action"`
	Output    string    `json:"output,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

type PlanStep struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Action      Action         `json:"action"`
	Target      string         `json:"target,omitempty"`
	Args        map[string]any `json:"args,omitempty"`
	Verify      []string       `json:"verify,omitempty"`
}

type Plan struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	Goal       string     `json:"goal"`
	Steps      []PlanStep `json:"steps"`
	Acceptance []string   `json:"acceptance,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type SessionState string

const (
	SessionPlanned    SessionState = "PLANNED"
	SessionExecuting  SessionState = "EXECUTING"
	SessionWaiting    SessionState = "WAITING_APPROVAL"
	SessionVerifying  SessionState = "VERIFYING"
	SessionRepairing  SessionState = "REPAIRING"
	SessionCompleted  SessionState = "COMPLETED"
	SessionFailed     SessionState = "FAILED"
	SessionCancelled  SessionState = "CANCELLED"
	SessionRolledBack SessionState = "ROLLED_BACK"
)

type Session struct {
	ID          string       `json:"id"`
	Goal        string       `json:"goal"`
	State       SessionState `json:"state"`
	Plan        *Plan        `json:"plan,omitempty"`
	CurrentStep int          `json:"current_step"`
	LastError   string       `json:"last_error,omitempty"`
	StartedAt   time.Time    `json:"started_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	CompletedAt time.Time    `json:"completed_at,omitempty"`
}
