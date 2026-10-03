package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MoveFile renames or relocates a regular file within the workspace.
// An existing destination, identical paths, and version-control internals
// are rejected so moves are always explicit and never destructive.
func (w *Workspace) MoveFile(source, destination string) (string, error) {
	sourceResolved, err := w.Resolve(source, true)
	if err != nil {
		return "", err
	}
	destinationResolved, err := w.Resolve(destination, false)
	if err != nil {
		return "", err
	}
	if sourceResolved == destinationResolved {
		return "", errors.New("source and destination are the same path")
	}
	sourceInfo, err := os.Stat(sourceResolved)
	if err != nil {
		return "", err
	}
	if !sourceInfo.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", source)
	}
	if _, err := os.Stat(destinationResolved); err == nil {
		return "", fmt.Errorf("destination %q already exists", destination)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	sourceRel, err := filepath.Rel(w.root, sourceResolved)
	if err != nil {
		return "", err
	}
	destinationRel, err := filepath.Rel(w.root, destinationResolved)
	if err != nil {
		return "", err
	}
	if skippedDir(filepath.ToSlash(sourceRel)) || skippedDir(filepath.ToSlash(destinationRel)) {
		return "", fmt.Errorf("refusing to move version-control path")
	}
	if err := w.beforeMoving(sourceResolved, destinationResolved); err != nil {
		return "", err
	}
	content, err := os.ReadFile(sourceResolved)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destinationResolved), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(sourceResolved, destinationResolved); err != nil {
		return "", err
	}
	moved, err := os.ReadFile(destinationResolved)
	if err != nil {
		return "", fmt.Errorf("verify moved file %q: %w", destination, err)
	}
	if string(moved) != string(content) {
		return "", fmt.Errorf("verify moved file %q: content does not match source", destination)
	}
	if _, err := os.Lstat(sourceResolved); !os.IsNotExist(err) {
		if err == nil {
			return "", fmt.Errorf("verify moved file %q: source still exists", source)
		}
		return "", fmt.Errorf("verify moved file %q: %w", source, err)
	}
	return fmt.Sprintf("moved %s to %s", source, destination), nil
}
