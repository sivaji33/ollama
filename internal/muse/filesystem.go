package muse

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FileSystem struct {
	Guard *PathGuard
}

func (f *FileSystem) List(path string) (string, error) {
	p, err := f.Guard.Resolve(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += string(os.PathSeparator)
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

func (f *FileSystem) Search(root, query string, limit int) (string, error) {
	p, err := f.Guard.Resolve(root)
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = 100
	}
	q := strings.ToLower(query)
	var matches []string
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if len(matches) >= limit {
			return fs.SkipAll
		}
		if d.IsDir() {
			switch strings.ToLower(d.Name()) {
			case ".git", "node_modules", ".venv", "venv", "__pycache__", "dist", "build":
				if path != p {
					return fs.SkipDir
				}
			}
			return nil
		}
		rel, _ := filepath.Rel(p, path)
		if q == "" || strings.Contains(strings.ToLower(rel), q) {
			matches = append(matches, rel)
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 256*1024 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil && bytes.Contains(bytes.ToLower(b), []byte(q)) {
			matches = append(matches, rel)
		}
		return nil
	})
	return strings.Join(matches, "\n"), err
}

func (f *FileSystem) Read(path string) (string, error) {
	p, err := f.Guard.Resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", p)
	}
	if info.Size() > 8*1024*1024 {
		return "", errors.New("file exceeds Muse text-read limit")
	}
	b, err := os.ReadFile(p)
	return string(b), err
}

func (f *FileSystem) Write(path, content string) (string, error) {
	p, err := f.Guard.Resolve(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp := p + ".muse-write"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return "written " + p, nil
}

func (f *FileSystem) Patch(path, oldText, newText string) (string, error) {
	p, err := f.Guard.Resolve(path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	old := []byte(oldText)
	if len(old) == 0 || bytes.Count(b, old) != 1 {
		return "", errors.New("patch requires exactly one non-empty match")
	}
	next := bytes.Replace(b, old, []byte(newText), 1)
	if bytes.Equal(b, next) {
		return "", errors.New("patch is a no-op")
	}
	return f.Write(p, string(next))
}

func (f *FileSystem) Delete(path string) (string, error) {
	p, err := f.Guard.Resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("recursive directory deletion is intentionally unsupported")
	}
	if err := os.Remove(p); err != nil {
		return "", err
	}
	return "deleted " + p, nil
}

func (f *FileSystem) Move(source, destination string) (string, error) {
	src, err := f.Guard.Resolve(source)
	if err != nil {
		return "", err
	}
	dst, err := f.Guard.Resolve(destination)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return "", errors.New("destination already exists")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return src + " -> " + dst, nil
}
