package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (w *Workspace) ApplyPatch(path, oldText, newText string) (string, error) {
	if oldText == newText {
		return "", errors.New("patch is a no-op")
	}
	resolved, err := w.Resolve(path, false)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(resolved)
	creating := os.IsNotExist(err) && oldText == ""
	if err != nil && !creating {
		return "", err
	}
	if len(b) > maxFileBytes {
		return "", fmt.Errorf("%q exceeds edit limit", path)
	}
	content := string(b)
	if count := strings.Count(content, oldText); !creating && count != 1 {
		return "", fmt.Errorf("old_text must match exactly once; matched %d times", count)
	}
	updated := newText
	if !creating {
		updated = strings.Replace(content, oldText, newText, 1)
	}
	if updated == content {
		return "", errors.New("patch is a no-op")
	}
	mode := os.FileMode(0o600)
	if !creating {
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return "", statErr
		}
		mode = info.Mode().Perm()
	}
	if err := w.beforeMutation(resolved); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), ".agent-edit-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		return "", err
	}
	written, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("verify patched file %q: %w", path, err)
	}
	if string(written) != updated {
		return "", fmt.Errorf("verify patched file %q: on-disk content does not match patch", path)
	}
	if creating {
		return fmt.Sprintf("created %s", path), nil
	}
	return fmt.Sprintf("updated %s", path), nil
}
