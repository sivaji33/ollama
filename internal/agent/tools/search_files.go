package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (w *Workspace) SearchFiles(query string, limit int) (string, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query = strings.ToLower(query)
	var matches []string
	err := filepath.WalkDir(w.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "build" || d.Name() == "node_modules") && path != w.root {
			return filepath.SkipDir
		}
		if d.IsDir() || len(matches) >= limit {
			return nil
		}
		rel, _ := filepath.Rel(w.root, path)
		if query == "" || strings.Contains(strings.ToLower(filepath.ToSlash(rel)), query) {
			matches = append(matches, filepath.ToSlash(rel))
			return nil
		}
		resolved, resolveErr := w.Resolve(rel, true)
		if resolveErr != nil {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > maxFileBytes {
			return nil
		}
		b, readErr := os.ReadFile(resolved)
		if readErr == nil && strings.Contains(strings.ToLower(string(b)), query) {
			matches = append(matches, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "no matches", nil
	}
	return fmt.Sprintf("%s", strings.Join(matches, "\n")), nil
}
