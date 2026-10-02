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
type Workspace struct{ root string }

func NewWorkspace(root string) (*Workspace, error) {
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
	return &Workspace{root: filepath.Clean(abs)}, nil
}

func (w *Workspace) Root() string { return w.root }

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
	if mustExist {
		if _, err := os.Stat(resolved); err != nil {
			return "", err
		}
	}
	return resolved, nil
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
