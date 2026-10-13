package muse

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

type CheckpointRecord struct {
	ID         string    `json:"id"`
	SourcePath string    `json:"source_path"`
	BackupPath string    `json:"backup_path"`
	SHA256     string    `json:"sha256"`
	CreatedAt  time.Time `json:"created_at"`
}

type CheckpointStore struct {
	Root  string
	Guard *PathGuard
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *CheckpointStore) Save(path string) (CheckpointRecord, error) {
	var rec CheckpointRecord
	src, err := c.Guard.Resolve(path)
	if err != nil {
		return rec, err
	}
	info, err := os.Stat(src)
	if err != nil {
		return rec, err
	}
	if info.IsDir() {
		return rec, errors.New("file checkpoint only")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return rec, err
	}
	id := newID("checkpoint")
	dst := filepath.Join(c.Root, id+"-"+filepath.Base(src)+".bak")
	if err := os.MkdirAll(c.Root, 0o700); err != nil {
		return rec, err
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return rec, err
	}
	sum, err := fileSHA256(dst)
	if err != nil {
		return rec, err
	}
	rec = CheckpointRecord{ID: id, SourcePath: src, BackupPath: dst, SHA256: sum, CreatedAt: time.Now().UTC()}
	return rec, nil
}

func (c *CheckpointStore) Restore(rec CheckpointRecord) (string, error) {
	dst, err := c.Guard.Resolve(rec.SourcePath)
	if err != nil {
		return "", err
	}
	sum, err := fileSHA256(rec.BackupPath)
	if err != nil {
		return "", err
	}
	if sum != rec.SHA256 {
		return "", errors.New("checkpoint hash mismatch")
	}
	b, err := os.ReadFile(rec.BackupPath)
	if err != nil {
		return "", err
	}
	tmp := dst + ".muse-restore"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dst, nil
}
