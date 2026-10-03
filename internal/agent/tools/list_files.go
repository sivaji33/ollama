package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListFiles returns regular files matching a glob pattern, bounded to limit
// entries. Relative patterns resolve against the workspace root; absolute
// patterns anywhere on the machine are allowed. Version-control and build
// directories are skipped.
func (w *Workspace) ListFiles(pattern string, limit int) (string, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		pattern = "*"
	}
	// Glob patterns contain metacharacters that must not be resolved through
	// the filesystem before matching, so the pattern is only made absolute.
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(w.root, pattern)
	}
	candidate, err := filepath.Abs(pattern)
	if err != nil {
		return "", err
	}
	matches, err := filepath.Glob(candidate)
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	var listed []string
	for _, match := range matches {
		// Each match is re-resolved through symlinks so a symlinked directory
		// still lists the real files it points at.
		resolved, resolveErr := filepath.EvalSymlinks(match)
		if resolveErr != nil {
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

// skippedDir reports whether a root-relative path traverses a directory that
// is never useful to the agent: version control, build output, or dependency
// caches.
func skippedDir(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		switch segment {
		case ".git", "build", "node_modules":
			return true
		}
	}
	return false
}
