package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

// DeleteFile removes a regular file beneath the workspace root. Directories,
// missing paths, and version-control internals are rejected.
func (w *Workspace) DeleteFile(path string) (string, error) {
	resolved, err := w.Resolve(path, true)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", path)
	}
	rel, err := filepath.Rel(w.root, resolved)
	if err != nil {
		return "", err
	}
	if skippedDir(filepath.ToSlash(rel)) {
		return "", fmt.Errorf("refusing to delete version-control path %q", path)
	}
	if err := os.Remove(resolved); err != nil {
		return "", err
	}
	return fmt.Sprintf("deleted %s", path), nil
}
