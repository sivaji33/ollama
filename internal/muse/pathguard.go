package muse

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type PathGuard struct {
	AllowedRoots []string
}

func NewPathGuard(roots ...string) (*PathGuard, error) {
	if len(roots) == 0 {
		return nil, errors.New("at least one allowed root is required")
	}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.Clean(abs))
	}
	return &PathGuard{AllowedRoots: out}, nil
}

func (g *PathGuard) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	for _, root := range g.AllowedRoots {
		rel, err := filepath.Rel(root, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return abs, nil
		}
	}
	return "", errors.New("path is outside Muse allowed roots")
}
