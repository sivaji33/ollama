package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
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

var (
	pathTokenRE = regexp.MustCompile(`(?i)(?:^|[\s"'=,(])([a-z]:[\\/][^\s"',;|)]+|/[A-Za-z0-9_.-][^\s"',;|)]*)`)
	cdRE        = regexp.MustCompile(`(?i)(?:^|[;&|]\s*|\n\s*)(?:cd|chdir|set-location|sl|pushd|push-location)\s+(?:-[A-Za-z]+\s+)?([^;&|\r\n]+)`)
)

func (w *Workspace) validateCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return errors.New("command is required")
	}
	for _, token := range strings.Fields(command) {
		clean := strings.Trim(token, `"'(),;`)
		if strings.Contains(filepath.ToSlash(clean), "../") || clean == ".." {
			return fmt.Errorf("command contains escaping traversal: %q", clean)
		}
	}
	for _, match := range pathTokenRE.FindAllStringSubmatch(command, -1) {
		p := strings.TrimSpace(match[1])
		if runtime.GOOS != "windows" && regexp.MustCompile(`^[A-Za-z]:`).MatchString(p) {
			return fmt.Errorf("outside-workspace absolute path rejected: %q", p)
		}
		if _, err := w.Resolve(p, false); err != nil {
			return fmt.Errorf("outside-workspace absolute path rejected: %q", p)
		}
	}
	for _, match := range cdRE.FindAllStringSubmatch(command, -1) {
		p := strings.Trim(strings.TrimSpace(match[1]), `"'`)
		if _, err := w.Resolve(p, false); err != nil {
			return fmt.Errorf("directory change rejected: %w", err)
		}
	}
	return nil
}

type outputBudget struct {
	mu             sync.Mutex
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
	if take > w.budget.remaining {
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

func (w *Workspace) RunShell(ctx context.Context, command string, timeout time.Duration, outputLimit int) (ShellResult, error) {
	if err := w.validateCommand(command); err != nil {
		return ShellResult{}, err
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if outputLimit <= 0 {
		outputLimit = 64 * 1024
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = w.root
	budget := &outputBudget{remaining: outputLimit}
	cmd.Stdout = budgetWriter{budget: budget}
	cmd.Stderr = budgetWriter{budget: budget, stderr: true}
	err := cmd.Run()
	result := ShellResult{Stdout: budget.stdout.String(), Stderr: budget.stderr.String(), Truncated: budget.truncated}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
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
