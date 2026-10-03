package selfimprove

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git is a thin wrapper around the git CLI scoped to one working tree. The
// self-improvement loop uses it to take a checkpoint commit before every cycle
// and to reset the tree back to that checkpoint when a cycle fails a guard, so
// a bad change can never outlive the cycle that produced it.
type Git struct {
	dir string
}

func NewGit(dir string) *Git { return &Git{dir: dir} }

func (g *Git) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, message)
	}
	return stdout.String(), nil
}

// checkpointCommitArgs pins a fixed identity and skips hooks. Pinning the
// identity keeps checkpoints working on a machine where git user.name and
// user.email were never configured, and --no-verify keeps an interactive hook
// from stalling an unattended loop.
func checkpointCommitArgs(message string) []string {
	return []string{
		"-c", "user.name=OwnBot Self-Improve",
		"-c", "user.email=ownbot@localhost",
		"commit", "--no-verify", "--no-gpg-sign", "-m", message,
	}
}

// RequireRepo fails unless dir is inside a git working tree. The loop depends on
// git for checkpoints, so it refuses to run anywhere it cannot undo its work.
func (g *Git) RequireRepo(ctx context.Context) error {
	out, err := g.run(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("self-improve needs a git repository to checkpoint its work: %w", err)
	}
	if strings.TrimSpace(out) != "true" {
		return fmt.Errorf("%s is not a git working tree; self-improve refuses to run without one", g.dir)
	}
	return nil
}

// Head returns the current commit, or "" in a repository with no commit yet.
func (g *Git) Head(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(err.Error(), "unknown revision") || strings.Contains(err.Error(), "ambiguous argument") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsDirty reports whether the working tree has staged or unstaged changes.
func (g *Git) IsDirty(ctx context.Context) (bool, error) {
	out, err := g.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Untracked lists repository-relative paths git is not tracking yet.
func (g *Git) Untracked(ctx context.Context) ([]string, error) {
	out, err := g.run(ctx, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// Commit stages everything and records a checkpoint. A tree with nothing to
// commit is not an error: the current commit is returned unchanged so callers
// can still use it as a reset target.
func (g *Git) Commit(ctx context.Context, message string) (string, error) {
	dirty, err := g.IsDirty(ctx)
	if err != nil {
		return "", err
	}
	if !dirty {
		return g.Head(ctx)
	}
	if _, err := g.run(ctx, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := g.run(ctx, checkpointCommitArgs(message)...); err != nil {
		return "", err
	}
	return g.Head(ctx)
}

// ChangedFiles lists the files that differ from since, including files the
// cycle created and never added to the index.
func (g *Git) ChangedFiles(ctx context.Context, since string) ([]string, error) {
	var changed []string
	if strings.TrimSpace(since) != "" {
		out, err := g.run(ctx, "diff", "--name-only", since)
		if err != nil {
			return nil, err
		}
		changed = append(changed, splitLines(out)...)
	}
	untracked, err := g.Untracked(ctx)
	if err != nil {
		return nil, err
	}
	changed = append(changed, untracked...)
	return dedupe(changed), nil
}

// RevertTo restores the tree to sha and removes the files the reverted cycle
// created. Files listed in preserveUntracked existed before the cycle, so they
// belong to the operator and are left alone.
func (g *Git) RevertTo(ctx context.Context, sha string, preserveUntracked []string) error {
	if strings.TrimSpace(sha) == "" {
		return errors.New("revert target revision is required")
	}
	if _, err := g.run(ctx, "reset", "--hard", sha); err != nil {
		return err
	}

	// A hard reset leaves newly created files behind because git never tracked
	// them. Remove only the ones this cycle introduced.
	preserve := make(map[string]struct{}, len(preserveUntracked))
	for _, path := range preserveUntracked {
		preserve[filepath.ToSlash(path)] = struct{}{}
	}
	untracked, err := g.Untracked(ctx)
	if err != nil {
		return err
	}
	for _, path := range untracked {
		if _, kept := preserve[filepath.ToSlash(path)]; kept {
			continue
		}
		full := filepath.Join(g.dir, filepath.FromSlash(path))
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove file created by reverted cycle %q: %w", path, err)
		}
	}
	return nil
}

func splitLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		key := filepath.ToSlash(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}
