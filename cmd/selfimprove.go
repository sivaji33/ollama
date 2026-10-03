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

	"github.com/ollama/ollama/api"
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

// SelfImproveHandler runs OwnBot's autonomous self-improvement loop. Every cycle
// researches a topic on the internet, edits this repository, and is kept only if
// the loop's own verification passes; otherwise the working tree is reset to the
// cycle's checkpoint.
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

	client, err := api.ClientFromEnvironment()
	if err != nil {
		return err
	}

	// Ctrl-C cancels cleanly between cycles instead of mid-write. The loop
	// already reverts an interrupted cycle.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "ownbot: "+format+"\n", args...)
	}

	logf("self-improve starting")
	logf("  workspace: %s", workspace)
	logf("  model:     %s", model)
	logf("  cycles:    %d   budget: %s", cycles, budget)
	logf("  verify:    %s", strings.Join(verify, " ; "))
	logf("  journal:   %s", journalPath)
	if researchOnly {
		logf("  mode:      research only (no file will be changed)")
	}
	logf("Every cycle is checkpointed before it runs and reset if it fails verification.")

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
		Long: `OwnBot researches a topic on the internet, edits this repository, and keeps the
change only if verification passes.

Every cycle is guarded:
  - the working tree is checkpointed before the cycle runs
  - the loop re-runs the verification commands itself, rather than trusting the
    model's claim that they passed
  - a cycle that leaves conflict markers, touches .git, or fails verification is
    reset back to its checkpoint

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
	selfImproveCmd.Flags().Bool("allow-dirty", false, "Checkpoint uncommitted work instead of refusing to start")
	selfImproveCmd.Flags().Bool("research-only", false, "Gather information without changing any file")
	selfImproveCmd.Flags().String("journal", "", "Journal file (default: ~/.ollama/self-improve/journal.jsonl)")

	return selfImproveCmd
}
