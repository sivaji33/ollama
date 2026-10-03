package agent

import (
	"context"
	"time"

	"github.com/ollama/ollama/api"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

const (
	// Deprecated: retained for source compatibility. The engine no longer
	// enforces fixed total-step, repair-attempt, command-timeout, or output
	// limits. Command budgets now come from the operator-tunable environment
	// variables documented in limits.go.
	DefaultMaxSteps       = 12
	MaxRepairAttempts     = 2
	DefaultCommandTimeout = 2 * time.Minute
	DefaultOutputLimit    = 64 * 1024
)

type Status string

const (
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

type RunRequest struct {
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model"`
	Workspace string `json:"workspace"`
	Task      string `json:"task"`
	// StepOffset is server-owned session history and is never accepted from JSON.
	StepOffset int `json:"-"`
	// Deprecated: accepted from existing clients, but ignored by the engine.
	MaxSteps             int                              `json:"max_steps,omitempty"`
	Verify               []string                         `json:"verify,omitempty"`
	OnLifecycleEvent     func(SessionEvent) error         `json:"-"`
	FilesystemCheckpoint *agenttools.FilesystemCheckpoint `json:"-"`
}

type ToolCallRecord struct {
	Step      int            `json:"step"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Output    string         `json:"output,omitempty"`
	Error     string         `json:"error,omitempty"`
}
type VerificationResult struct {
	Command   string `json:"command"`
	Passed    bool   `json:"passed"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type StepKind string

const (
	StepToolCall      StepKind = "tool_call"
	StepToolFreeText  StepKind = "tool_free_text"
	StepToolFreeEmpty StepKind = "tool_free_empty"
	StepEditDetected  StepKind = "edit_detected"
	StepVerification  StepKind = "verification"
	StepRepair        StepKind = "repair"
)

type StepEvent struct {
	Step     int      `json:"step"`
	Kind     StepKind `json:"kind"`
	ToolName string   `json:"tool_name,omitempty"`
	Passed   *bool    `json:"passed,omitempty"`
}

type RunResult struct {
	SessionID           string               `json:"session_id"`
	Status              Status               `json:"status"`
	StepsExecuted       int                  `json:"steps_executed"`
	ContextBuilt        bool                 `json:"context_built,omitempty"`
	ToolCalls           []ToolCallRecord     `json:"tool_calls"`
	ChangedFiles        []string             `json:"changed_files"`
	GitDiff             string               `json:"git_diff"`
	VerificationResults []VerificationResult `json:"verification_results"`
	StepEvents          []StepEvent          `json:"step_events"`
	LifecycleEvents     []SessionEvent       `json:"lifecycle_events,omitempty"`
	FinalSummary        string               `json:"final_summary"`
}

type ChatClient interface {
	Chat(context.Context, *api.ChatRequest, api.ChatResponseFunc) error
}
