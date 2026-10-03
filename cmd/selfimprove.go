package cmd

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ollama/ollama/internal/agent"
	"github.com/ollama/ollama/internal/selfimprove"
)

// selfImproveDefaultVerify is the gate every cycle must keep passing. It is the
// same quick build the repository's own AGENTS.md documents for Go-only
// iteration, so a kept cycle can never leave the tree uncompilable.
var selfImproveDefaultVerify = []string{"go build ./..."}

// defaultJournalPath puts the journal in the user's own directory rather than in
// the workspace. A journal written inside the repository would make the working
// tree dirty, and the loop refuses to start on a dirty tree.
func defaultJournalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ollama", "self-improve", "journal.jsonl"), nil
}

// SelfImproveHandler runs a local-model transaction against the live workspace.
// Each cycle is retained only if its filesystem diff passes independent
// verification; otherwise its filesystem checkpoint is restored.
func SelfImproveHandler(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()

	workspace, err := flags.GetString("workspace")
	if err != nil {
		return err
	}
	if strings.TrimSpace(workspace) == "" {
		workspace, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	if workspace, err = filepath.Abs(workspace); err != nil {
		return err
	}

	model, err := flags.GetString("model")
	if err != nil {
		return err
	}
	if model != selfimprove.CustomRuntimeModel {
		return fmt.Errorf("self-improve requires the configured local model %q; refusing model %q", selfimprove.CustomRuntimeModel, model)
	}
	cycles, err := flags.GetInt("cycles")
	if err != nil {
		return err
	}
	budget, err := flags.GetDuration("budget")
	if err != nil {
		return err
	}
	verify, err := flags.GetStringArray("verify")
	if err != nil {
		return err
	}
	topics, err := flags.GetStringArray("topic")
	if err != nil {
		return err
	}
	build, err := flags.GetString("build")
	if err != nil {
		return err
	}
	allowDirty, err := flags.GetBool("allow-dirty")
	if err != nil {
		return err
	}
	researchOnly, err := flags.GetBool("research-only")
	if err != nil {
		return err
	}
	journalPath, err := flags.GetString("journal")
	if err != nil {
		return err
	}
	if strings.TrimSpace(journalPath) == "" {
		if journalPath, err = defaultJournalPath(); err != nil {
			return fmt.Errorf("resolve default journal path: %w", err)
		}
	}

	journal, err := openJournal(journalPath)
	if err != nil {
		return err
	}
	defer journal.Close()

	// Ctrl-C cancels cleanly between cycles instead of mid-write. The loop
	// already reverts an interrupted cycle.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	client, runtimeDiagnostic, err := selfimprove.VerifyCustomRuntime(ctx)
	if err != nil {
		return err
	}

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "ownbot: "+format+"\n", args...)
	}

	logf("self-improve starting")
	logf("  workspace: %s", workspace)
	logf("  model:     %s", model)
	logf("  runtime:   %s (PID %d; inference verified)", runtimeDiagnostic.Endpoint, runtimeDiagnostic.PID)
	logf("  cycles:    %d   budget: %s", cycles, budget)
	logf("  verify:    %s", strings.Join(verify, " ; "))
	logf("  journal:   %s", journalPath)
	if researchOnly {
		logf("  mode:      research only (no file will be changed)")
	}
	logf("Every cycle checkpoints live files before editing; failed verification restores the filesystem checkpoint.")

	cfg := selfimprove.Config{
		Workspace:    workspace,
		Model:        model,
		Verify:       verify,
		Topics:       topics,
		Cycles:       cycles,
		Budget:       budget,
		Build:        build,
		ResearchOnly: researchOnly,
		AllowDirty:   allowDirty,
		Logf:         logf,
		Journal:      journal,
	}

	result, err := selfimprove.Run(ctx, cfg, agent.NewEngine(client))
	if err != nil {
		return err
	}

	logf("self-improve finished: %d kept, %d reverted, %s", result.Kept, result.Reverted, result.Stopped)
	return printSelfImproveSummary(os.Stdout, result)
}

func openJournal(path string) (io.WriteCloser, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create journal directory %s: %w", dir, err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open journal %s: %w", path, err)
	}
	return file, nil
}

// printSelfImproveSummary writes one readable line per cycle so an operator can
// see exactly what was kept, what was undone, and why.
func printSelfImproveSummary(out io.Writer, result selfimprove.SessionResult) error {
	for _, record := range result.Cycles {
		line := fmt.Sprintf("#%d %-8s %s", record.Index, record.Outcome, record.Summary)
		if record.Reason != "" {
			line += " | " + record.Reason
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
		if len(record.Changed) > 0 {
			if _, err := fmt.Fprintf(out, "     changed: %s\n", strings.Join(record.Changed, ", ")); err != nil {
				return err
			}
		}
	}
	return nil
}

// newSelfImproveCmd builds the self-improve command. It is registered in NewCLI
// alongside the other commands.
func newSelfImproveCmd() *cobra.Command {
	selfImproveCmd := &cobra.Command{
		Use:   "self-improve",
		Short: "Let OwnBot research the internet and improve its own source",
		Long: `OwnBot uses the configured local model to edit the live workspace and keeps the
change only if filesystem-based verification passes.

Every cycle is guarded:
  - files are checkpointed immediately before agent-tool mutations
  - the filesystem diff is calculated from actual on-disk contents
  - verification and optional build commands run independently of the model
  - failed transactions restore and verify the filesystem checkpoint
  - Git is optional and is never used to commit or reset source changes

Use --research-only to gather information without changing any file.`,
		Args: cobra.NoArgs,
		RunE: SelfImproveHandler,
	}

	selfImproveCmd.Flags().String("model", agent.DefaultAgentModel, "Model that performs each cycle")
	selfImproveCmd.Flags().Int("cycles", 3, "Number of improvement cycles to attempt")
	selfImproveCmd.Flags().Duration("budget", time.Hour, "Wall-clock budget for the whole session")
	selfImproveCmd.Flags().StringArray("verify", selfImproveDefaultVerify, "Command that must pass for a cycle to be kept (repeatable)")
	selfImproveCmd.Flags().StringArray("topic", selfimprove.DefaultTopics(), "Research topic for a cycle, in order (repeatable)")
	selfImproveCmd.Flags().String("workspace", "", "Repository to improve (default: current directory)")
	selfImproveCmd.Flags().String("build", "", "Extra command that must pass to keep a cycle, such as a compile step")
	selfImproveCmd.Flags().Bool("allow-dirty", false, "Deprecated: live filesystem transactions checkpoint their own changes")
	selfImproveCmd.Flags().Bool("research-only", false, "Gather information without changing any file")
	selfImproveCmd.Flags().String("journal", "", "Journal file (default: ~/.ollama/self-improve/journal.jsonl)")

	return selfImproveCmd
}
