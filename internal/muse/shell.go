package muse

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Shell struct {
	WorkDir string
	Timeout time.Duration
}

func (s *Shell) Run(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-lc", command)
	}
	cmd.Dir = s.WorkDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
