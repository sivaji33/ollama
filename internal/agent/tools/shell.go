package tools

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type ShellResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

type outputBudget struct {
	mu             sync.Mutex
	unlimited      bool
	remaining      int
	truncated      bool
	stdout, stderr bytes.Buffer
}
type budgetWriter struct {
	budget *outputBudget
	stderr bool
}

func (w budgetWriter) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	n := len(p)
	take := n
	if !w.budget.unlimited && take > w.budget.remaining {
		take = w.budget.remaining
		w.budget.truncated = true
	}
	if take > 0 {
		if w.stderr {
			_, _ = w.budget.stderr.Write(p[:take])
		} else {
			_, _ = w.budget.stdout.Write(p[:take])
		}
		w.budget.remaining -= take
	}
	return n, nil
}

// RunShell executes command through the platform shell with the workspace root
// as the working directory. Commands are not restricted to the workspace:
// absolute paths, traversal, and directory changes anywhere on the machine are
// permitted by design so the agent can also act on installs outside the
// repository.
//
// timeout < 0 and outputLimit < 0 mean "no limit": the command runs until it
// finishes or the context is cancelled, and every byte it writes is captured.
// Zero keeps the conservative defaults (2 minutes, 64 KiB).
func (w *Workspace) RunShell(ctx context.Context, command string, timeout time.Duration, outputLimit int) (ShellResult, error) {
	if strings.TrimSpace(command) == "" {
		return ShellResult{}, errors.New("command is required")
	}
	runCtx := ctx
	if timeout >= 0 {
		if timeout == 0 {
			timeout = 2 * time.Minute
		}
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	budget := &outputBudget{unlimited: outputLimit < 0}
	if !budget.unlimited {
		if outputLimit == 0 {
			outputLimit = 64 * 1024
		}
		budget.remaining = outputLimit
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(runCtx, "sh", "-c", command)
	}
	cmd.Dir = w.root
	cmd.Stdout = budgetWriter{budget: budget}
	cmd.Stderr = budgetWriter{budget: budget, stderr: true}
	err := cmd.Run()
	result := ShellResult{Stdout: budget.stdout.String(), Stderr: budget.stderr.String(), Truncated: budget.truncated}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return result, nil
		}
		return result, err
	}
	return result, nil
}
