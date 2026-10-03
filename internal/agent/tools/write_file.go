package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile creates a new file or atomically replaces the full content of an
// existing file beneath the workspace root. A write that would not change the
// file is rejected as a no-op so every success represents a real change.
func (w *Workspace) WriteFile(path, content string) (string, error) {
	resolved, err := w.Resolve(path, false)
	if err != nil {
		return "", err
	}
	if len(content) > maxFileBytes {
		return "", fmt.Errorf("%q exceeds %d byte write limit", path, maxFileBytes)
	}
	existing, readErr := os.ReadFile(resolved)
	creating := os.IsNotExist(readErr)
	if readErr != nil && !creating {
		return "", readErr
	}
	mode := os.FileMode(0o600)
	if !creating {
		if string(existing) == content {
			return "", errors.New("write is a no-op")
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return "", statErr
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%q is not a regular file", path)
		}
		mode = info.Mode().Perm()
	}
	if err := w.beforeMutation(resolved); err != nil {
		return "", err
	}
	if err := writeFileAtomic(resolved, content, mode); err != nil {
		return "", err
	}
	written, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("verify written file %q: %w", path, err)
	}
	if string(written) != content {
		return "", fmt.Errorf("verify written file %q: on-disk content does not match requested content", path)
	}
	if creating {
		return fmt.Sprintf("created %s", path), nil
	}
	return fmt.Sprintf("rewrote %s", path), nil
}

// writeFileAtomic replaces the file at resolved with content using a
// same-directory temporary file and rename, so readers never observe a
// partially written file.
func writeFileAtomic(resolved, content string, mode os.FileMode) error {
	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agent-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, resolved)
}
