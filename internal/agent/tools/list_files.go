package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListFiles returns workspace-relative regular files matching a glob pattern,
// bounded to limit entries. Version-control and build directories are skipped.
func (w *Workspace) ListFiles(pattern string, limit int) (string, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		pattern = "*"
	}
	// Containment is validated lexically: glob patterns contain metacharacters
	// that must not be resolved through the filesystem before matching.
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(w.root, pattern)
	}
	candidate, err := filepath.Abs(pattern)
	if err != nil {
		return "", err
	}
	if !contained(w.root, candidate) {
		return "", fmt.Errorf("pattern %q is outside workspace", pattern)
	}
	matches, err := filepath.Glob(candidate)
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	var listed []string
	for _, match := range matches {
		// Each match is re-resolved through symlinks so escapes are caught
		// before anything is listed.
		resolved, resolveErr := filepath.EvalSymlinks(match)
		if resolveErr != nil || !contained(w.root, resolved) {
			continue
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		rel, relErr := filepath.Rel(w.root, resolved)
		if relErr != nil || skippedDir(filepath.ToSlash(rel)) {
			continue
		}
		if len(listed) >= limit {
			return fmt.Sprintf("%s\n... and %d more", strings.Join(listed, "\n"), len(matches)-len(listed)), nil
		}
		listed = append(listed, filepath.ToSlash(rel))
	}
	if len(listed) == 0 {
		return "no matches", nil
	}
	return strings.Join(listed, "\n"), nil
}

// skippedDir reports whether a workspace-relative path traverses a directory
// that is never useful to the agent: version control, build output, or
// dependency caches.
func skippedDir(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		switch segment {
		case ".git", "build", "node_modules":
			return true
		}
	}
	return false
}
