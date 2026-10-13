package muse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type SelfImprover struct {
	SourceRoot   string
	CandidateDir string
}

func (s *SelfImprover) StageCandidate(_ context.Context) (string, error) {
	if s.SourceRoot == "" || s.CandidateDir == "" {
		return "", errors.New("source and candidate directories are required")
	}
	dst := filepath.Join(s.CandidateDir, "candidate-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := copyTree(s.SourceRoot, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o700)
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".venv", "venv", "node_modules", "__pycache__", "dist", "build":
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > 32*1024*1024 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
}
