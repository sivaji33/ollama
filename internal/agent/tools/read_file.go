package tools

import (
	"fmt"
	"os"
)

func (w *Workspace) ReadFile(path string) (string, error) {
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
	if info.Size() > maxFileBytes {
		return "", fmt.Errorf("%q exceeds %d byte read limit", path, maxFileBytes)
	}
	b, err := os.ReadFile(resolved)
	return string(b), err
}
