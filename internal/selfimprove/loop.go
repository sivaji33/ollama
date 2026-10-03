// Package selfimprove runs OwnBot's autonomous self-improvement loop.
//
// Each cycle researches one topic on the internet, edits this repository, and
// verifies the result. The loop never trusts the model's own claim of success:
// it re-runs the verification commands itself, refuses to keep a cycle that
// left conflict markers or touched version-control internals, and resets the
// working tree to the cycle's checkpoint commit on any failure.
//
// The guardrails are the point of the package rather than an add-on. An
// unattended loop that edits its own source must not be able to leave the
// repository in a state that cannot build: commit fa6dad5c in this repository
// is the concrete example, where a half-finished merge was left in the source
// and broke the build.
package selfimprove

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/ollama/ollama/internal/agent"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

// Runner runs a single agent task. *agent.Engine satisfies this interface; it
// is an interface so the loop can be tested without a live model.
type Runner interface {
	Run(ctx context.Context, request agent.RunRequest) (agent.RunResult, error)
}

// Config describes one self-improvement session. Workspace, Model and Verify
// are required; every other field has a usable default.
type Config struct {
	// Workspace is the repository the loop improves. It must be a git working
	// tree, because checkpoints and reverts are how the loop undoes a cycle.
	Workspace string
	// Model is the model that performs each cycle.
	Model string
	// Verify commands must all exit 0 before a cycle may be kept. They are run
	// by the loop itself, independently of the agent's own verification.
	Verify []string
	// Topics are the research topics, used in order and then repeated.
	Topics []string
	// Cycles is the number of improvement cycles to attempt.
	Cycles int
	// Budget caps the session's wall-clock time.
	Budget time.Duration
	// Build is an optional shell command run after verification, typically a
	// compile or vet step, that must also exit 0 for the cycle to be kept.
	Build string
	// ResearchOnly makes every cycle gather information without changing files.
	ResearchOnly bool
	// AllowDirty lets the loop start on a tree with uncommitted work by first
	// committing that work as a checkpoint. Without it a dirty tree is refused,
	// because reverting would otherwise discard unsaved work.
	AllowDirty bool
	// Logf receives human-readable progress. Defaults to discarding output.
	Logf func(format string, args ...any)
	// Journal receives one JSON line per cycle.
	Journal io.Writer
	// Now is injectable so tests can control the budget clock.
	Now func() time.Time
}

// CycleOutcome is what the loop did with one cycle's change.
type CycleOutcome string

const (
	// OutcomeKept means the change passed every guard and was committed.
	OutcomeKept CycleOutcome = "kept"
	// OutcomeReverted means a guard rejected the change and it was undone.
	OutcomeReverted CycleOutcome = "reverted"
	// OutcomeSkipped means the cycle never ran, usually because the repository
	// was not in a state where a checkpoint could be taken.
	OutcomeSkipped CycleOutcome = "skipped"
)

// CycleRecord is the journal entry for one cycle.
type CycleRecord struct {
	Index      int          `json:"index"`
	Topic      string       `json:"topic"`
	Outcome    CycleOutcome `json:"outcome"`
	Reason     string       `json:"reason,omitempty"`
	Summary    string       `json:"summary,omitempty"`
	HeadBefore string       `json:"head_before,omitempty"`
	HeadAfter  string       `json:"head_after,omitempty"`
	Changed    []string     `json:"changed_files,omitempty"`
	DurationMS int64        `json:"duration_ms"`
	StartedAt  time.Time    `json:"started_at"`
}

// SessionResult summarises a whole session.
type SessionResult struct {
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

// Run drives the loop until the cycle count, the time budget, or the context is
// exhausted. A cycle that fails any guard is reverted before the next cycle
// starts, so the repository is always left in a state that passed verification.
func Run(ctx context.Context, cfg Config, runner Runner) (SessionResult, error) {
	var result SessionResult
	if runner == nil {
		return result, errors.New("self-improve requires an agent runner")
	}

	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return result, err
	}

	shell, err := agenttools.NewWorkspace(cfg.Workspace)
	if err != nil {
		return result, err
	}

	l := &loop{cfg: cfg, runner: runner, git: NewGit(cfg.Workspace), shell: shell, now: cfg.Now}

	if err := l.git.RequireRepo(ctx); err != nil {
		return result, err
	}
	if err := l.prepare(ctx); err != nil {
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

		record := l.runCycle(ctx, index, cfg.Topics[(index-1)%len(cfg.Topics)], deadline)
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

// prepare makes the tree safe to checkpoint. A dirty tree is refused by default
// because the loop reverts by resetting to a commit, which would discard work
// the operator had not saved yet.
func (l *loop) prepare(ctx context.Context) error {
	dirty, err := l.git.IsDirty(ctx)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	if !l.cfg.AllowDirty {
		return errors.New("workspace has uncommitted changes; commit or stash them first, or re-run with --allow-dirty to have the loop checkpoint them")
	}
	sha, err := l.git.Commit(ctx, "ownbot self-improve: checkpoint existing work")
	if err != nil {
		return err
	}
	l.cfg.Logf("checkpointed existing uncommitted work as %s", shortSHA(sha))
	return nil
}

func (l *loop) writeJournal(record CycleRecord) {
	if l.cfg.Journal == nil {
		return
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	_, _ = l.cfg.Journal.Write(append(encoded, '\n'))
}

func shortSHA(sha string) string {
	if sha == "" {
		return "(no commit)"
	}
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// oneLine flattens a model summary so it can be used as a commit subject.
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

// runCycle performs one improvement cycle: checkpoint, research and edit, guard,
// then either commit the change or reset the tree back to the checkpoint.
func (l *loop) runCycle(ctx context.Context, index int, topic string, deadline time.Time) CycleRecord {
	started := l.now()
	record := CycleRecord{Index: index, Topic: topic, StartedAt: started, Changed: []string{}}

	headBefore, err := l.git.Head(ctx)
	if err != nil {
		return l.skip(record, started, err.Error())
	}
	record.HeadBefore = headBefore
	record.HeadAfter = headBefore

	// Record the files the operator already had, so a revert never deletes work
	// that predates the cycle.
	preserve, err := l.git.Untracked(ctx)
	if err != nil {
		return l.skip(record, started, err.Error())
	}

	// Checkpoint first: this makes the whole cycle undoable with a single reset,
	// including files the agent creates that git never tracked.
	checkpoint, err := l.git.Commit(ctx, fmt.Sprintf("ownbot self-improve: checkpoint before cycle %d", index))
	if err != nil {
		return l.skip(record, started, err.Error())
	}
	l.cfg.Logf("cycle %d: checkpoint %s", index, shortSHA(checkpoint))
	l.cfg.Logf("cycle %d: topic: %s", index, topic)

	cycleCtx := ctx
	if remaining := deadline.Sub(l.now()); remaining > 0 {
		var cancel context.CancelFunc
		cycleCtx, cancel = context.WithTimeout(ctx, remaining)
		defer cancel()
	}

	run, runErr := l.runner.Run(cycleCtx, agent.RunRequest{
		Model:     l.cfg.Model,
		Workspace: l.cfg.Workspace,
		Task:      buildTask(topic, l.cfg.Verify, l.cfg.ResearchOnly),
		Verify:    l.cfg.Verify,
	})
	record.Summary = strings.TrimSpace(run.FinalSummary)

	if l.cfg.ResearchOnly {
		changed, _ := l.git.ChangedFiles(ctx, checkpoint)
		if len(changed) == 0 {
			return l.skip(record, started, "research only; no change applied")
		}
		record.Reason = "research cycle changed files; reverted"
		if err := l.git.RevertTo(ctx, checkpoint, preserve); err != nil {
			record.Reason += "; revert failed: " + err.Error()
		}
		return l.revert(record, started)
	}

	if reason := l.reject(cycleCtx, checkpoint, run, runErr); reason != "" {
		record.Reason = reason
		if err := l.git.RevertTo(ctx, checkpoint, preserve); err != nil {
			record.Reason += "; revert failed: " + err.Error()
		}
		l.cfg.Logf("cycle %d: reverted - %s", index, reason)
		return l.revert(record, started)
	}

	changed, err := l.git.ChangedFiles(ctx, checkpoint)
	if err != nil {
		record.Reason = "inspect changed files: " + err.Error()
		_ = l.git.RevertTo(ctx, checkpoint, preserve)
		return l.revert(record, started)
	}
	record.Changed = changed

	kept, err := l.git.Commit(ctx, fmt.Sprintf("ownbot self-improve cycle %d: %s", index, oneLine(record.Summary)))
	if err != nil {
		record.Reason = "keep commit failed: " + err.Error()
		_ = l.git.RevertTo(ctx, checkpoint, preserve)
		return l.revert(record, started)
	}
	record.HeadAfter = kept
	record.Outcome = OutcomeKept
	record.DurationMS = l.now().Sub(started).Milliseconds()
	l.cfg.Logf("cycle %d: kept as %s - %s", index, shortSHA(kept), oneLine(record.Summary))
	return record
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

// reject returns the reason a cycle must not be kept, or "" when the change is
// safe to keep. The model's own success claim is never sufficient: every gate
// here is evaluated by the loop itself.
func (l *loop) reject(ctx context.Context, checkpoint string, run agent.RunResult, runErr error) string {
	if runErr != nil {
		return "agent run failed: " + runErr.Error()
	}
	if run.Status != agent.StatusSuccess {
		return "agent did not complete: " + oneLine(run.FinalSummary)
	}

	changed, err := l.git.ChangedFiles(ctx, checkpoint)
	if err != nil {
		return "inspect changed files: " + err.Error()
	}
	if len(changed) == 0 {
		return "agent reported success without changing any file"
	}

	// Unfinished-merge guard. This is the defect that broke this repository in
	// commit fa6dad5c, so it is checked before anything else is trusted.
	if markers := findConflictMarkers(l.cfg.Workspace, changed); len(markers) > 0 {
		return fmt.Sprintf("conflict markers left in %s:%d", markers[0].Path, markers[0].Line)
	}

	// Version-control internals are never a legitimate target for a cycle.
	if path := touchesGitInternals(changed); path != "" {
		return "cycle modified version-control internals: " + path
	}

	// Independent verification: re-run the commands rather than trusting the
	// agent's own report that they passed.
	results, passed := l.verify(ctx)
	if !passed {
		for _, result := range results {
			if !result.Passed {
				return fmt.Sprintf("independent verification failed: %s (exit %d)", result.Command, result.ExitCode)
			}
		}
		return "independent verification failed"
	}

	if build := strings.TrimSpace(l.cfg.Build); build != "" {
		shell, err := l.shell.RunShell(ctx, build, -1, -1)
		if err != nil {
			return "build command could not run: " + err.Error()
		}
		if shell.ExitCode != 0 {
			return fmt.Sprintf("build command failed: %s (exit %d)", build, shell.ExitCode)
		}
	}
	return ""
}

func (l *loop) verify(ctx context.Context) ([]agent.VerificationResult, bool) {
	results := make([]agent.VerificationResult, 0, len(l.cfg.Verify))
	passed := true
	for _, command := range l.cfg.Verify {
		shell, err := l.shell.RunShell(ctx, command, -1, -1)
		result := agent.VerificationResult{
			Command:   command,
			ExitCode:  shell.ExitCode,
			Stdout:    shell.Stdout,
			Stderr:    shell.Stderr,
			Truncated: shell.Truncated,
			Passed:    err == nil && shell.ExitCode == 0,
		}
		if !result.Passed {
			passed = false
		}
		results = append(results, result)
	}
	return results, passed
}

// touchesGitInternals reports the first changed path that lives inside the
// repository metadata directory. A cycle must never modify git's own state.
func touchesGitInternals(files []string) string {
	for _, file := range files {
		slash := filepath.ToSlash(file)
		if slash == ".git" || strings.HasPrefix(slash, ".git/") || strings.Contains(slash, "/.git/") {
			return slash
		}
	}
	return ""
}
