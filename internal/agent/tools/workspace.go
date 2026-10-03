package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxFileBytes = 256 * 1024

// Workspace selects the default working directory and the base for relative
// paths. It is deliberately not a containment boundary: absolute paths,
// traversal, and symlinks anywhere on the machine resolve normally so the
// agent can also inspect and update files outside the repository, such as an
// installed program it was asked to upgrade.
type Workspace struct {
	root         string
	restricted   bool
	beforeChange func(string) error
	beforeMove   func(string, string) error
}

func NewWorkspace(root string) (*Workspace, error) {
	return newWorkspace(root, false)
}

func NewRestrictedWorkspace(root string) (*Workspace, error) {
	return newWorkspace(root, true)
}

func newWorkspace(root string, restricted bool) (*Workspace, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("workspace must be a directory")
	}
	return &Workspace{root: filepath.Clean(abs), restricted: restricted}, nil
}

func (w *Workspace) Root() string { return w.root }

func (w *Workspace) SetBeforeMutation(before func(string) error) {
	w.beforeChange = before
}

func (w *Workspace) SetBeforeMove(before func(string, string) error) {
	w.beforeMove = before
}

func (w *Workspace) beforeMutation(path string) error {
	if w.beforeChange == nil {
		return nil
	}
	return w.beforeChange(path)
}

func (w *Workspace) beforeMoving(source, destination string) error {
	if w.beforeMove != nil {
		return w.beforeMove(source, destination)
	}
	if err := w.beforeMutation(source); err != nil {
		return err
	}
	return w.beforeMutation(destination)
}

// Resolve returns the cleaned absolute path for path. Relative paths resolve
// against the workspace root; absolute paths are used as given. No path is
// rejected for being outside the workspace.
func (w *Workspace) Resolve(path string, mustExist bool) (string, error) {
	if w == nil || w.root == "" {
		return "", errors.New("invalid workspace")
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(w.root, candidate)
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	resolved, err := resolveExistingPrefix(candidate)
	if err != nil {
		return "", err
	}
	if err := w.validateRestrictedPath(resolved, path); err != nil {
		return "", err
	}
	if mustExist {
		if _, err := os.Stat(resolved); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

func (w *Workspace) validateRestrictedPath(resolved, display string) error {
	if !w.restricted {
		return nil
	}
	rel, err := filepath.Rel(w.root, resolved)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path %q is outside the configured workspace", display)
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.EqualFold(part, ".git") {
			return fmt.Errorf("refusing access to version-control path %q", display)
		}
	}
	return nil
}

func resolveExistingPrefix(path string) (string, error) {
	cur := filepath.Clean(path)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}
