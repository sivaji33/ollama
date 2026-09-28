package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileCreatesNewFileAndRewritesExisting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	got, err := w.WriteFile("nested/new.go", "package nested\n")
	if err != nil || got != "created nested/new.go" {
		t.Fatalf("create: %q %v", got, err)
	}
	got, err = w.WriteFile("main.go", "package main\n")
	if err != nil || got != "rewrote main.go" {
		t.Fatalf("rewrite: %q %v", got, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "package main\n" {
		t.Fatalf("got %q", b)
	}
}

func TestWriteFileRejectsNoopOversizeAndEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	if _, err := w.WriteFile("main.go", "package main\n"); err == nil {
		t.Fatal("expected no-op rejection")
	}
	if _, err := w.WriteFile("big.go", strings.Repeat("x", maxFileBytes+1)); err == nil {
		t.Fatal("expected oversize rejection")
	}
	if _, err := w.WriteFile(filepath.Join("..", "outside.txt"), "x"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestListFilesMatchesGlobAndSkipsBuildDirs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"main.go":           "package main\n",
		"internal/a.go":     "package a\n",
		"internal/b.go":     "package b\n",
		".git/config":       "[core]\n",
		"build/out.go":      "package build\n",
		"node_modules/x.js": "module.exports = 1\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w, _ := NewWorkspace(root)
	got, err := w.ListFiles("internal/*.go", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got != "internal/a.go\ninternal/b.go" {
		t.Fatalf("got %q", got)
	}
	top, err := w.ListFiles("", 100)
	if err != nil {
		t.Fatal(err)
	}
	if top != "main.go" {
		t.Fatalf("got %q", top)
	}
}

func TestListFilesBoundsResults(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%d.go", i)), []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w, _ := NewWorkspace(root)
	got, err := w.ListFiles("*.go", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "... and 3 more") {
		t.Fatalf("got %q", got)
	}
}

func TestDeleteFileRemovesRegularFileOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gone.go"), []byte("package gone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	if _, err := w.DeleteFile("gone.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone.go")); !os.IsNotExist(err) {
		t.Fatal("expected file removal")
	}
	if _, err := w.DeleteFile(".git/config"); err == nil {
		t.Fatal("expected version-control rejection")
	}
	if _, err := w.DeleteFile("docs"); err == nil {
		t.Fatal("expected directory rejection")
	}
	if _, err := w.DeleteFile("missing.go"); err == nil {
		t.Fatal("expected missing rejection")
	}
}

func TestMoveFileRelocatesAndGuards(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.go"), []byte("package old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "taken.go"), []byte("package taken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dest.go"), []byte("package dest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	if _, err := w.MoveFile("old.go", "internal/new/old.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "new", "old.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.go")); !os.IsNotExist(err) {
		t.Fatal("expected source removal")
	}
	if _, err := w.MoveFile("taken.go", "dest.go"); err == nil {
		t.Fatal("expected destination rejection")
	}
	if _, err := w.MoveFile("missing.go", "x.go"); err == nil {
		t.Fatal("expected missing source rejection")
	}
}

func TestMultiEditAppliesAllEdits(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package old\n\nfunc a() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	if _, err := w.MultiEdit("main.go", []Edit{
		{OldText: "package old", NewText: "package main"},
		{OldText: "func a() {}", NewText: "func a() int { return 1 }"},
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "package main\n\nfunc a() int { return 1 }\n" {
		t.Fatalf("got %q", b)
	}
}

func TestMultiEditIsAllOrNothing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package old\n\nfunc a() {}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	if _, err := w.MultiEdit("main.go", []Edit{
		{OldText: "package old", NewText: "package main"},
		{OldText: "func missing() {}", NewText: "func present() {}"},
	}); err == nil {
		t.Fatal("expected failure")
	}
	b, _ := os.ReadFile(path)
	if string(b) != original {
		t.Fatalf("file changed despite failure: %q", b)
	}
}

func TestMultiEditRejectsNoopAndAmbiguousMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "dup.txt"), []byte("same same same\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	if _, err := w.MultiEdit("dup.txt", []Edit{{OldText: "same", NewText: "same"}}); err == nil {
		t.Fatal("expected no-op rejection")
	}
	if _, err := w.MultiEdit("dup.txt", []Edit{{OldText: "same", NewText: "other"}}); err == nil {
		t.Fatal("expected ambiguous match rejection")
	}
	if _, err := w.MultiEdit("dup.txt", nil); err == nil {
		t.Fatal("expected empty edits rejection")
	}
}
