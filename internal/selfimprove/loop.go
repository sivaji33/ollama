// Package selfimprove runs OwnBot's live filesystem self-development loop.
package selfimprove

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ollama/ollama/internal/agent"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

type Runner interface {
	Run(ctx context.Context, request agent.RunRequest) (agent.RunResult, error)
}

type Config struct {
	TransactionID string
	Workspace     string
	Model         string
	Verify        []string
	Topics        []string
	Cycles        int
	Budget        time.Duration
	Build         string
	ResearchOnly  bool
	AllowDirty    bool
	Logf          func(format string, args ...any)
	Journal       io.Writer
	OnEvent       func(TransactionEvent) error
	OnAgentEvent  func(agent.SessionEvent) error
	Now           func() time.Time
}

type TransactionEvent struct {
	ID        string                     `json:"id"`
	Stage     string                     `json:"stage"`
	Status    string                     `json:"status"`
	ToolName  string                     `json:"tool_name,omitempty"`
	Path      string                     `json:"path,omitempty"`
	Diff      *agenttools.FilesystemDiff `json:"filesystem_diff,omitempty"`
	Message   string                     `json:"message"`
	Timestamp time.Time                  `json:"timestamp"`
}

type CycleOutcome string

const (
	OutcomeKept     CycleOutcome = "kept"
	OutcomeReverted CycleOutcome = "reverted"
	OutcomeSkipped  CycleOutcome = "skipped"
)

type CycleRecord struct {
	ID            string                    `json:"id"`
	Index         int                       `json:"index"`
	Topic         string                    `json:"topic"`
	Outcome       CycleOutcome              `json:"outcome"`
	Reason        string                    `json:"reason,omitempty"`
	Summary       string                    `json:"summary,omitempty"`
	HeadBefore    string                    `json:"head_before,omitempty"`
	HeadAfter     string                    `json:"head_after,omitempty"`
	Changed       []string                  `json:"changed_files,omitempty"`
	Diff          agenttools.FilesystemDiff `json:"filesystem_diff"`
	FilesRetained int                       `json:"files_retained"`
	FilesRestored int                       `json:"files_restored"`
	DurationMS    int64                     `json:"duration_ms"`
	StartedAt     time.Time                 `json:"started_at"`
}

type SessionResult struct {
	ID       string        `json:"id"`
	Cycles   []CycleRecord `json:"cycles"`
	Kept     int           `json:"kept"`
	Reverted int           `json:"reverted"`
	Stopped  string        `json:"stopped,omitempty"`
}

type loop struct {
	cfg    Config
	runner Runner
	git    *Git
	shell  *agenttools.Workspace
	now    func() time.Time
}

func Run(ctx context.Context, cfg Config, runner Runner) (SessionResult, error) {
	var result SessionResult
	if runner == nil {
		return result, errors.New("self-improve requires an agent runner")
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return result, err
	}
	shell, err := agenttools.NewRestrictedWorkspace(cfg.Workspace)
	if err != nil {
		return result, err
	}
	l := &loop{cfg: cfg, runner: runner, git: NewGit(cfg.Workspace), shell: shell, now: cfg.Now}
	result.ID = cfg.TransactionID
	if result.ID == "" {
		result.ID = "selfup_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if err := l.emit(result.ID, "workspace", "COMPLETED", "live workspace verified"); err != nil {
		return result, err
	}

	deadline := l.now().Add(cfg.Budget)
	for index := 1; index <= cfg.Cycles; index++ {
		if ctx.Err() != nil {
			result.Stopped = "cancelled"
			break
		}
		if l.now().After(deadline) {
			result.Stopped = fmt.Sprintf("time budget of %s reached", cfg.Budget)
			break
		}
		record := l.runCycle(ctx, result.ID, index, cfg.Topics[(index-1)%len(cfg.Topics)], deadline)
		result.Cycles = append(result.Cycles, record)
		switch record.Outcome {
		case OutcomeKept:
			result.Kept++
		case OutcomeReverted:
			result.Reverted++
		}
		l.writeJournal(record)
		if ctx.Err() != nil {
			result.Stopped = "cancelled"
			break
		}
	}
	if result.Stopped == "" {
		result.Stopped = fmt.Sprintf("completed %d cycle(s)", len(result.Cycles))
	}
	return result, nil
}

func (c Config) withDefaults() Config {
	if c.Topics == nil {
		c.Topics = DefaultTopics()
	}
	if c.Cycles <= 0 {
		c.Cycles = 1
	}
	if c.Budget <= 0 {
		c.Budget = time.Hour
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
	return c
}

func (c Config) validate() error {
	if strings.TrimSpace(c.Workspace) == "" {
		return errors.New("self-improve requires a workspace")
	}
	if strings.TrimSpace(c.Model) == "" {
		return errors.New("self-improve requires a model")
	}
	if len(c.Topics) == 0 {
		return errors.New("self-improve requires at least one topic")
	}
	if len(c.Verify) == 0 {
		return errors.New("self-improve requires at least one verification command")
	}
	return nil
}

func (l *loop) writeJournal(record CycleRecord) {
	if l.cfg.Journal == nil {
		return
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		l.cfg.Logf("encode self-development journal entry: %v", err)
		return
	}
	if _, err := l.cfg.Journal.Write(append(encoded, '\n')); err != nil {
		l.cfg.Logf("write self-development journal entry: %v", err)
	}
}

func oneLine(summary string) string {
	flat := strings.Join(strings.Fields(summary), " ")
	if flat == "" {
		return "no summary"
	}
	const limit = 120
	if len(flat) > limit {
		return flat[:limit] + "..."
	}
	return flat
}

func (l *loop) runCycle(ctx context.Context, id string, index int, topic string, deadline time.Time) CycleRecord {
	started := l.now()
	record := CycleRecord{ID: id, Index: index, Topic: topic, StartedAt: started, Changed: []string{}}
	if err := l.emit(id, "checkpoint", "EXECUTING", "creating live filesystem checkpoint"); err != nil {
		return l.skip(record, started, err.Error())
	}
	checkpoint, err := agenttools.NewFilesystemCheckpoint(l.cfg.Workspace)
	if err != nil {
		return l.skip(record, started, "create filesystem checkpoint: "+err.Error())
	}
	defer func() {
		if err := checkpoint.Close(); err != nil {
			l.cfg.Logf("remove filesystem checkpoint: %v", err)
		}
	}()

	gitStateBefore, err := l.git.GitMetadataHash(ctx)
	if err != nil {
		return l.skip(record, started, "inspect optional git safety metadata: "+err.Error())
	}
	gitMetadataBefore := ""
	if gitStateBefore != "" {
		gitMetadataBefore, err = l.git.SnapshotMetadata()
		if err != nil {
			return l.skip(record, started, "snapshot optional git safety metadata: "+err.Error())
		}
		defer func() {
			if err := os.RemoveAll(gitMetadataBefore); err != nil {
				l.cfg.Logf("remove optional git metadata checkpoint: %v", err)
			}
		}()
	}
	if err := l.emit(id, "checkpoint", "COMPLETED", "live filesystem checkpoint created"); err != nil {
		return l.skip(record, started, err.Error())
	}
	l.cfg.Logf("cycle %d: topic: %s", index, topic)

	cycleCtx := ctx
	if remaining := deadline.Sub(l.now()); remaining > 0 {
		var cancel context.CancelFunc
		cycleCtx, cancel = context.WithTimeout(ctx, remaining)
		defer cancel()
	}
	if err := l.emit(id, "agent", "EXECUTING", "starting local coding agent"); err != nil {
		record.Reason = err.Error()
		l.rollback(checkpoint, gitMetadataBefore, &record)
		return l.revert(record, started)
	}
	run, runErr := l.runner.Run(cycleCtx, agent.RunRequest{
		Model:                l.cfg.Model,
		Workspace:            l.cfg.Workspace,
		Task:                 buildTask(topic, l.cfg.Verify, l.cfg.ResearchOnly),
		FilesystemCheckpoint: checkpoint,
		OnLifecycleEvent:     l.cfg.OnAgentEvent,
	})
	record.Summary = strings.TrimSpace(run.FinalSummary)
	if runErr != nil {
		_ = l.emit(id, "agent", "FAILED", runErr.Error())
	} else {
		_ = l.emit(id, "agent", "COMPLETED", "coding agent returned")
	}

	if err := l.emit(id, "diff", "EXECUTING", "collecting actual live filesystem diff"); err != nil {
		record.Reason = err.Error()
		l.rollback(checkpoint, gitMetadataBefore, &record)
		return l.revert(record, started)
	}
	diff, err := checkpoint.Diff()
	if err != nil {
		record.Reason = "collect actual filesystem diff: " + err.Error()
		l.rollback(checkpoint, gitMetadataBefore, &record)
		return l.revert(record, started)
	}
	record.Diff = diff
	record.Changed = diffFilePaths(diff)
	if err := l.emit(id, "diff", "COMPLETED", fmt.Sprintf("filesystem diff contains %d file(s), +%d/-%d lines", len(diff.Files), diff.Additions, diff.Deletions)); err != nil {
		record.Reason = err.Error()
		l.rollback(checkpoint, gitMetadataBefore, &record)
		return l.revert(record, started)
	}

	if l.cfg.ResearchOnly {
		if len(diff.Files) == 0 {
			return l.skip(record, started, "research only; no change applied")
		}
		record.Reason = "research-only cycle changed files; restored"
		l.rollback(checkpoint, gitMetadataBefore, &record)
		return l.revert(record, started)
	}
	if reason := l.reject(cycleCtx, id, gitStateBefore, run, runErr, diff); reason != "" {
		record.Reason = reason
		l.rollback(checkpoint, gitMetadataBefore, &record)
		l.cfg.Logf("cycle %d: rolled back - %s", index, reason)
		return l.revert(record, started)
	}
	if err := l.emit(id, "retain", "EXECUTING", "retaining verified live filesystem changes"); err != nil {
		record.Reason = err.Error()
		l.rollback(checkpoint, gitMetadataBefore, &record)
		return l.revert(record, started)
	}
	record.FilesRetained = len(diff.Files)
	record.Outcome = OutcomeKept
	record.DurationMS = l.now().Sub(started).Milliseconds()
	if err := l.emit(id, "retain", "COMPLETED", "verified changes retained in the live workspace"); err != nil {
		record.Reason = "changes retained, but publishing completion failed: " + err.Error()
	}
	l.cfg.Logf("cycle %d: retained live filesystem changes - %s", index, oneLine(record.Summary))
	return record
}

func (l *loop) rollback(checkpoint *agenttools.FilesystemCheckpoint, gitMetadataBefore string, record *CycleRecord) {
	_ = l.emit(record.ID, "rollback", "EXECUTING", "restoring filesystem checkpoint")
	restored, err := checkpoint.Rollback()
	record.FilesRestored = len(restored)
	if err != nil {
		record.Reason += "; filesystem rollback failed: " + err.Error()
		_ = l.emit(record.ID, "rollback", "FAILED", err.Error())
		return
	}
	if gitMetadataBefore != "" {
		if err := l.git.RestoreMetadata(gitMetadataBefore); err != nil {
			record.Reason += "; optional git metadata restore failed: " + err.Error()
			_ = l.emit(record.ID, "rollback", "FAILED", err.Error())
			return
		}
	}
	record.FilesRetained = 0
	_ = l.emit(record.ID, "rollback", "COMPLETED", fmt.Sprintf("restored and verified %d filesystem path(s)", len(restored)))
}

func (l *loop) emit(id, stage, status, message string) error {
	event := TransactionEvent{ID: id, Stage: stage, Status: status, Message: message, Timestamp: l.now().UTC()}
	if l.cfg.OnEvent != nil {
		if err := l.cfg.OnEvent(event); err != nil {
			return fmt.Errorf("publish self-development event %s: %w", stage, err)
		}
	}
	l.cfg.Logf("%s %s: %s", status, stage, message)
	return nil
}

func (l *loop) skip(record CycleRecord, started time.Time, reason string) CycleRecord {
	record.Outcome = OutcomeSkipped
	record.Reason = reason
	record.DurationMS = l.now().Sub(started).Milliseconds()
	return record
}

func (l *loop) revert(record CycleRecord, started time.Time) CycleRecord {
	record.Outcome = OutcomeReverted
	record.DurationMS = l.now().Sub(started).Milliseconds()
	return record
}

func (l *loop) reject(ctx context.Context, id, gitStateBefore string, run agent.RunResult, runErr error, diff agenttools.FilesystemDiff) string {
	if runErr != nil {
		return "agent run failed: " + runErr.Error()
	}
	if run.Status != agent.StatusSuccess {
		return "agent did not complete: " + oneLine(run.FinalSummary)
	}
	if gitStateBefore != "" {
		gitStateAfter, err := l.git.GitMetadataHash(ctx)
		if err != nil {
			return "inspect optional git metadata: " + err.Error()
		}
		if gitStateAfter != gitStateBefore {
			return "cycle modified version-control internals: .git metadata changed"
		}
	}
	if len(diff.Files) == 0 {
		return "No live filesystem change detected."
	}
	changed := diffFilePaths(diff)
	if path := touchesGitInternals(changed); path != "" {
		return "cycle modified version-control internals: " + path
	}
	if markers := findConflictMarkers(l.cfg.Workspace, changed); len(markers) > 0 {
		return fmt.Sprintf("conflict markers left in %s:%d", markers[0].Path, markers[0].Line)
	}
	results, passed := l.verify(ctx, id)
	if !passed {
		for _, result := range results {
			if !result.Passed {
				return fmt.Sprintf("independent verification failed: %s (exit %d)", result.Command, result.ExitCode)
			}
		}
		return "independent verification failed"
	}
	if build := strings.TrimSpace(l.cfg.Build); build != "" {
		if err := l.emit(id, "build", "EXECUTING", build); err != nil {
			return err.Error()
		}
		shell, err := l.shell.RunShell(ctx, build, -1, -1)
		if err != nil {
			return "build command could not run: " + err.Error()
		}
		if shell.ExitCode != 0 {
			_ = l.emit(id, "build", "FAILED", fmt.Sprintf("%s (exit %d)", build, shell.ExitCode))
			return fmt.Sprintf("build command failed: %s (exit %d)", build, shell.ExitCode)
		}
		if err := l.emit(id, "build", "COMPLETED", build); err != nil {
			return err.Error()
		}
	}
	return ""
}

func (l *loop) verify(ctx context.Context, id string) ([]agent.VerificationResult, bool) {
	results := make([]agent.VerificationResult, 0, len(l.cfg.Verify))
	passed := true
	for _, command := range l.cfg.Verify {
		if err := l.emit(id, "verification", "EXECUTING", command); err != nil {
			results = append(results, agent.VerificationResult{Command: command, Passed: false, ExitCode: -1, Stderr: err.Error()})
			passed = false
			continue
		}
		shell, err := l.shell.RunShell(ctx, command, -1, -1)
		result := agent.VerificationResult{
			Command:   command,
			ExitCode:  shell.ExitCode,
			Stdout:    shell.Stdout,
			Stderr:    shell.Stderr,
			Truncated: shell.Truncated,
			Passed:    err == nil && shell.ExitCode == 0,
		}
		results = append(results, result)
		if !result.Passed {
			passed = false
			_ = l.emit(id, "verification", "FAILED", fmt.Sprintf("%s (exit %d)", command, shell.ExitCode))
		} else {
			_ = l.emit(id, "verification", "COMPLETED", command)
		}
	}
	return results, passed
}

func diffFilePaths(diff agenttools.FilesystemDiff) []string {
	paths := make([]string, 0, len(diff.Files)*2)
	for _, file := range diff.Files {
		if file.Status != "move_source" {
			paths = append(paths, file.Path)
		}
		if file.OldPath != "" {
			paths = append(paths, file.OldPath)
		}
	}
	return paths
}

func touchesGitInternals(files []string) string {
	for _, file := range files {
		slash := filepath.ToSlash(file)
		if slash == ".git" || strings.HasPrefix(slash, ".git/") || strings.Contains(slash, "/.git/") {
			return slash
		}
	}
	return ""
}
