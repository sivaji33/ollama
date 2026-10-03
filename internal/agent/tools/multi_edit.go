package tools

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const maxMultiEdits = 50

// Edit is one exact-text replacement within a file.
type Edit struct {
	OldText string
	NewText string
}

// MultiEdit applies a sequence of exact-text replacements to one file as a
// single all-or-nothing operation. Every edit is validated against the
// original content before anything is written: each old_text must match
// exactly once in the content remaining after earlier edits, and no edit may
// be a no-op. If any edit fails validation, the file is left untouched.
func (w *Workspace) MultiEdit(path string, edits []Edit) (string, error) {
	if len(edits) == 0 {
		return "", errors.New("at least one edit is required")
	}
	if len(edits) > maxMultiEdits {
		return "", fmt.Errorf("at most %d edits are allowed per call", maxMultiEdits)
	}
	resolved, err := w.Resolve(path, true)
	if err != nil {
		return "", err
	}
	original, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	if len(original) > maxFileBytes {
		return "", fmt.Errorf("%q exceeds edit limit", path)
	}
	mode, err := regularFileMode(resolved)
	if err != nil {
		return "", err
	}
	content := string(original)
	for i, edit := range edits {
		if edit.OldText == edit.NewText {
			return "", fmt.Errorf("edit %d is a no-op", i+1)
		}
		if count := strings.Count(content, edit.OldText); count != 1 {
			return "", fmt.Errorf("edit %d old_text must match exactly once; matched %d times", i+1, count)
		}
		updated := strings.Replace(content, edit.OldText, edit.NewText, 1)
		if updated == content {
			return "", fmt.Errorf("edit %d is a no-op", i+1)
		}
		content = updated
	}
	if content == string(original) {
		return "", errors.New("multi_edit produced no change")
	}
	if err := w.beforeMutation(resolved); err != nil {
		return "", err
	}
	if err := writeFileAtomic(resolved, content, mode); err != nil {
		return "", err
	}
	written, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("verify multi-edit file %q: %w", path, err)
	}
	if string(written) != content {
		return "", fmt.Errorf("verify multi-edit file %q: on-disk content does not match edits", path)
	}
	return fmt.Sprintf("applied %d edits to %s", len(edits), path), nil
}

// regularFileMode returns the permission bits of an existing regular file.
func regularFileMode(resolved string) (os.FileMode, error) {
	info, err := os.Stat(resolved)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("path is not a regular file")
	}
	return info.Mode().Perm(), nil
}
