package selfimprove

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ollama/ollama/internal/agent"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

// verifyOK always exits 0; verifyFail always exits non-zero. Both are plain git
// commands so the tests do not depend on a compiler being present.
const (
	verifyOK   = "git version"
	verifyFail = "git rev-parse --verify refs/heads/ownbot-missing-ref"
)

type fakeRunner struct {
	result   agent.RunResult
	err      error
	onRun    func(workspace string, checkpoint *agenttools.FilesystemCheckpoint)
	calls    int
	requests []agent.RunRequest
}

func (f *fakeRunner) Run(_ context.Context, request agent.RunRequest) (agent.RunResult, error) {
	f.calls++
	f.requests = append(f.requests, request)
	if f.onRun != nil {
		f.onRun(request.Workspace, request.FilesystemCheckpoint)
	}
	result := f.result
	if result.Status == "" {
		result.Status = agent.StatusSuccess
	}
	if result.FinalSummary == "" {
		result.FinalSummary = "applied a verified change"
	}
	return result, f.err
}

func writeCheckpointed(t *testing.T, workspace string, checkpoint *agenttools.FilesystemCheckpoint, name, content string) {
	t.Helper()
	path := filepath.Join(workspace, filepath.FromSlash(name))
	if err := checkpoint.BeforeMutation(path); err != nil {
		t.Fatal(err)
	}
	writeFile(t, workspace, name, content)
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "--quiet")
	// Pin line-ending handling so a checkout returns exactly the bytes that were
	// written. Without this, git's autocrlf rewrites LF to CRLF on Windows and
	// byte-for-byte restore assertions become platform dependent.
	gitIn(t, dir, "config", "core.autocrlf", "false")
	gitIn(t, dir, "config", "core.eol", "lf")
	writeFile(t, dir, "main.go", "package main\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "-c", "user.name=Test", "-c", "user.email=test@localhost",
		"commit", "--no-verify", "--no-gpg-sign", "-m", "initial")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil
}

func headSubject(t *testing.T, dir string) string {
	t.Helper()
	return gitIn(t, dir, "log", "-1", "--format=%s")
}

// baseConfig is a session that cannot touch the clock budget, so tests always
// run their requested number of cycles.
func baseConfig(workspace string) Config {
	return Config{
		Workspace: workspace,
		Model:     "test-model",
		Verify:    []string{verifyOK},
		Topics:    []string{"improve error handling"},
		Cycles:    1,
		Budget:    time.Hour,
	}
}

func TestRunKeepsVerifiedChange(t *testing.T) {
	dir := initRepo(t)
	headBefore := gitIn(t, dir, "rev-parse", "HEAD")
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		writeCheckpointed(t, workspace, checkpoint, "main.go", "package main\n\nfunc main() {}\n")
	}}

	result, err := Run(context.Background(), baseConfig(dir), runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept != 1 {
		t.Fatalf("kept = %d, want 1 (cycles: %+v)", result.Kept, result.Cycles)
	}
	if got := readFile(t, dir, "main.go"); !strings.Contains(got, "func main()") {
		t.Fatalf("verified change was not kept: %q", got)
	}
	if head := gitIn(t, dir, "rev-parse", "HEAD"); head != headBefore {
		t.Fatalf("HEAD changed from %q to %q; retaining source must not require a commit", headBefore, head)
	}
}

func TestRunWorksWithoutGitRepository(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	cfg := baseConfig(dir)
	cfg.Verify = []string{"exit 0"}
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		writeCheckpointed(t, workspace, checkpoint, "main.go", "package main\n\nfunc main() {}\n")
	}}
	result, err := Run(context.Background(), cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept != 1 {
		t.Fatalf("kept = %d, want 1 (cycles: %+v)", result.Kept, result.Cycles)
	}
	if got := readFile(t, dir, "main.go"); !strings.Contains(got, "func main()") {
		t.Fatalf("live change not retained in the non-Git workspace: %q", got)
	}
}

func TestRunRevertsChangeThatFailsVerification(t *testing.T) {
	dir := initRepo(t)
	headBefore := gitIn(t, dir, "rev-parse", "HEAD")
	cfg := baseConfig(dir)
	cfg.Verify = []string{verifyFail}
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		writeCheckpointed(t, workspace, checkpoint, "main.go", "package main\n\n// broken\n")
	}}

	result, err := Run(context.Background(), cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reverted != 1 {
		t.Fatalf("reverted = %d, want 1 (cycles: %+v)", result.Reverted, result.Cycles)
	}
	if got := readFile(t, dir, "main.go"); got != "package main\n" {
		t.Fatalf("file was not restored to its pre-cycle content: %q", got)
	}
	// A reverted cycle leaves the history exactly where it started: the
	// checkpoint is a reset target, not necessarily a new commit (a clean tree
	// has nothing to checkpoint).
	if head := gitIn(t, dir, "rev-parse", "HEAD"); head != headBefore {
		t.Fatalf("HEAD = %q, want the pre-cycle commit %q", head, headBefore)
	}
	if subject := headSubject(t, dir); strings.Contains(subject, "self-improve cycle") {
		t.Fatalf("head subject = %q, a reverted change must not be committed", subject)
	}
}

func TestRunRevertsConflictMarkers(t *testing.T) {
	dir := initRepo(t)
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		writeCheckpointed(t, workspace, checkpoint, "main.go",
			"package main\n\n<<<<<<< HEAD\nvar a = 1\n=======\nvar a = 2\n>>>>>>> other\n")
	}}

	result, err := Run(context.Background(), baseConfig(dir), runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reverted != 1 {
		t.Fatalf("reverted = %d, want 1 (cycles: %+v)", result.Reverted, result.Cycles)
	}
	if reason := result.Cycles[0].Reason; !strings.Contains(reason, "conflict markers") {
		t.Fatalf("reason = %q, want a conflict-marker rejection", reason)
	}
	if got := readFile(t, dir, "main.go"); strings.Contains(got, "<<<<<<<") {
		t.Fatalf("conflicted file was kept: %q", got)
	}
}

func TestRunRejectsAgentSuccessWithoutChange(t *testing.T) {
	dir := initRepo(t)
	result, err := Run(context.Background(), baseConfig(dir), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reverted != 1 {
		t.Fatalf("reverted = %d, want 1 (cycles: %+v)", result.Reverted, result.Cycles)
	}
	if reason := result.Cycles[0].Reason; !strings.Contains(reason, "No live filesystem change detected.") {
		t.Fatalf("reason = %q, want the no-change rejection", reason)
	}
}

func TestRunRejectsGitMetadataMutation(t *testing.T) {
	dir := initRepo(t)
	cfg := baseConfig(dir)
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		_ = checkpoint
		path := filepath.Join(workspace, ".git", "HEAD")
		if err := os.WriteFile(path, []byte("ref: refs/heads/other\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}}

	result, err := Run(context.Background(), cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reverted != 1 {
		t.Fatalf("reverted = %d, want 1 (cycles: %+v)", result.Reverted, result.Cycles)
	}
	if reason := result.Cycles[0].Reason; !strings.Contains(reason, "version-control internals") {
		t.Fatalf("reason = %q, want git-metadata rejection", reason)
	}
	if got, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	} else if !strings.HasPrefix(string(got), "ref: refs/heads/master") && !strings.HasPrefix(string(got), "ref: refs/heads/main") {
		t.Fatalf("HEAD = %q, want the repository HEAD restored after revert", got)
	}
}

func TestRunRestoresDirtyTreeWithoutLosingOperatorChanges(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, dir, "main.go", "package main\n\n// uncommitted work\n")
	cfg := baseConfig(dir)
	cfg.Verify = []string{verifyFail}
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		writeCheckpointed(t, workspace, checkpoint, "main.go", "package main\n\n// transaction edit\n")
	}}

	result, err := Run(context.Background(), cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reverted != 1 || runner.calls != 1 {
		t.Fatalf("result = %+v; runner calls = %d; want one rolled-back transaction", result, runner.calls)
	}
	if got := readFile(t, dir, "main.go"); got != "package main\n\n// uncommitted work\n" {
		t.Fatalf("operator's dirty work was not restored: %q", got)
	}
}

func TestRunCheckpointsDirtyTreeWhenAllowed(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, dir, "main.go", "package main\n\n// uncommitted work\n")

	cfg := baseConfig(dir)
	cfg.AllowDirty = true
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		writeCheckpointed(t, workspace, checkpoint, "main.go", "package main\n\n// uncommitted work\n// improvement\n")
	}}

	result, err := Run(context.Background(), cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept != 1 {
		t.Fatalf("kept = %d, want 1 (cycles: %+v)", result.Kept, result.Cycles)
	}
	got := readFile(t, dir, "main.go")
	if !strings.Contains(got, "// uncommitted work") {
		t.Fatalf("the operator's uncommitted work was lost: %q", got)
	}
	if !strings.Contains(got, "// improvement") {
		t.Fatalf("the kept change is missing: %q", got)
	}
}

func TestRunRevertRemovesCycleFilesAndKeepsOperatorFiles(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, dir, "notes.txt", "operator notes\n")

	cfg := baseConfig(dir)
	cfg.AllowDirty = true
	cfg.Verify = []string{verifyFail}
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		writeCheckpointed(t, workspace, checkpoint, "generated.go", "package main\n")
		writeCheckpointed(t, workspace, checkpoint, "main.go", "package main\n\n// edited\n")
	}}

	result, err := Run(context.Background(), cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reverted != 1 {
		t.Fatalf("reverted = %d, want 1 (cycles: %+v)", result.Reverted, result.Cycles)
	}
	if exists(dir, "generated.go") {
		t.Fatal("a file created by the reverted cycle was left behind")
	}
	if !exists(dir, "notes.txt") {
		t.Fatal("the revert deleted a file belonging to the operator")
	}
	if got := readFile(t, dir, "main.go"); got != "package main\n" {
		t.Fatalf("edited file was not restored: %q", got)
	}
}

func TestRunStopsAtTimeBudget(t *testing.T) {
	dir := initRepo(t)
	cfg := baseConfig(dir)
	cfg.Cycles = 3
	cfg.Budget = time.Minute

	base := time.Unix(0, 0)
	var spent atomic.Bool
	cfg.Now = func() time.Time {
		if spent.Load() {
			return base.Add(2 * time.Hour)
		}
		return base
	}

	var cycle int
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		cycle++
		writeCheckpointed(t, workspace, checkpoint, "main.go", fmt.Sprintf("package main\n\n// change %d\n", cycle))
		// Spend the whole budget during the first cycle.
		spent.Store(true)
	}}

	result, err := Run(context.Background(), cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cycles) != 1 {
		t.Fatalf("cycles = %d, want 1: the budget must stop the loop", len(result.Cycles))
	}
	if !strings.Contains(result.Stopped, "budget") {
		t.Fatalf("stopped = %q, want a budget explanation", result.Stopped)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
}

func TestRunWritesJournalEntry(t *testing.T) {
	dir := initRepo(t)
	var journal bytes.Buffer
	cfg := baseConfig(dir)
	cfg.Journal = &journal
	runner := &fakeRunner{onRun: func(workspace string, checkpoint *agenttools.FilesystemCheckpoint) {
		writeCheckpointed(t, workspace, checkpoint, "main.go", "package main\n\n// journaled\n")
	}}

	if _, err := Run(context.Background(), cfg, runner); err != nil {
		t.Fatal(err)
	}

	var record CycleRecord
	if err := json.Unmarshal(bytes.TrimSpace(journal.Bytes()), &record); err != nil {
		t.Fatalf("journal entry is not one JSON line: %v (%q)", err, journal.String())
	}
	if record.Outcome != OutcomeKept || record.Index != 1 {
		t.Fatalf("record = %+v, want cycle 1 kept", record)
	}
	if len(record.Changed) != 1 || record.Changed[0] != "main.go" {
		t.Fatalf("changed files = %v, want [main.go]", record.Changed)
	}
}

func TestFindConflictMarkers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "merged.go", "package main\n\n<<<<<<< HEAD\nvar a = 1\n=======\nvar a = 2\n>>>>>>> other\n")
	writeFile(t, dir, "clean.go", "package main\n")
	// Tokenizer fixtures contain runs of equals signs and bare angle brackets but
	// never a complete marker block, so they must not read as an unfinished merge.
	writeFile(t, dir, "vocab.bpe", "======= =\n<<<<<<< <\n>>>>>>> >\n")

	found := findConflictMarkers(dir, []string{"merged.go", "clean.go", "vocab.bpe", "missing.go"})
	if len(found) != 1 {
		t.Fatalf("found = %+v, want exactly one marker", found)
	}
	if found[0].Path != "merged.go" || found[0].Line != 3 {
		t.Fatalf("found = %+v, want merged.go:3", found[0])
	}
}

func TestTouchesGitInternals(t *testing.T) {
	if got := touchesGitInternals([]string{"main.go", "internal/agent/engine.go", "gitignore"}); got != "" {
		t.Fatalf("touchesGitInternals rejected an ordinary path: %q", got)
	}
	for _, path := range []string{".git", ".git/config", "sub/.git/HEAD"} {
		if got := touchesGitInternals([]string{"main.go", path}); got == "" {
			t.Fatalf("touchesGitInternals accepted %q", path)
		}
	}
}

func TestBuildTaskStatesTheRules(t *testing.T) {
	edit := buildTask("research X", []string{"go build ./..."}, false)
	for _, want := range []string{"web_search", "web_fetch", "conflict marker", "go build ./...", "smallest coherent change", "do not stop after the first successful edit", "after all edits"} {
		if !strings.Contains(edit, want) {
			t.Fatalf("task is missing %q:\n%s", want, edit)
		}
	}
	if strings.Contains(edit, "never delete a file you did not create") {
		t.Fatalf("task must permit requested file deletions:\n%s", edit)
	}

	research := buildTask("research X", []string{"go build ./..."}, true)
	if !strings.Contains(research, "Change nothing") {
		t.Fatalf("research-only task must forbid changes:\n%s", research)
	}
	if strings.Contains(research, "go build ./...") {
		t.Fatalf("research-only task must not list verification gates:\n%s", research)
	}
}
