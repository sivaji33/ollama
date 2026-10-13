package muse

import (
	"context"
	"os/exec"
	"strings"
)

type Git struct {
	WorkDir string
}

func (g *Git) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.WorkDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (g *Git) Status(ctx context.Context) (string, error) {
	return g.run(ctx, "status", "--porcelain=v1", "--branch")
}

func (g *Git) Diff(ctx context.Context) (string, error) {
	return g.run(ctx, "diff", "--")
}

func (g *Git) Commit(ctx context.Context, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", nil
	}
	return g.run(ctx, "commit", "-am", message)
}

func (g *Git) Push(ctx context.Context) (string, error) {
	return g.run(ctx, "push")
}

func (g *Git) Checkout(ctx context.Context, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", nil
	}
	return g.run(ctx, "checkout", ref)
}
