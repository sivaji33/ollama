package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceRejectsPathsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	w, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Resolve(filepath.Join(root, "..", "outside.txt"), false); err == nil {
		t.Fatal("expected outside absolute path to be rejected")
	}
	if _, err := w.Resolve(filepath.Join("..", "outside.txt"), false); err == nil {
		t.Fatal("expected escaping traversal to be rejected")
	}
}

func TestWorkspaceRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires Windows privilege")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	if _, err := w.Resolve("link/file.txt", false); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func TestReadFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	got, err := w.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if got != "package main\n" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyPatchCreatesRealChangeAndRejectsNoop(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	if _, err := w.ApplyPatch("main.go", "package old", "package new"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "package new\n" {
		t.Fatalf("got %q", b)
	}
	if _, err := w.ApplyPatch("main.go", "package new", "package new"); err == nil {
		t.Fatal("expected no-op rejection")
	}
}

func TestApplyPatchCreatesNewFile(t *testing.T) {
	w, _ := NewWorkspace(t.TempDir())
	if _, err := w.ApplyPatch("new.go", "", "package example\n"); err != nil {
		t.Fatal(err)
	}
	got, err := w.ReadFile("new.go")
	if err != nil {
		t.Fatal(err)
	}
	if got != "package example\n" {
		t.Fatalf("got %q", got)
	}
}

func TestShellRunsInWorkspaceAndCapturesExit(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWorkspace(root)
	command := "pwd"
	if runtime.GOOS == "windows" {
		command = "(Get-Location).Path"
	}
	result, err := w.RunShell(context.Background(), command, 5*time.Second, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(strings.ToLower(result.Stdout), strings.ToLower(filepath.Base(root))) {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestShellRejectsOutsidePathsAndTraversal(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWorkspace(root)
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	for _, command := range []string{"type " + outside, "cd ..", "Get-Content ../outside.txt"} {
		if _, err := w.RunShell(context.Background(), command, time.Second, 1024); err == nil {
			t.Fatalf("expected rejection for %q", command)
		}
	}
}

func TestShellTruncatesOutput(t *testing.T) {
	w, _ := NewWorkspace(t.TempDir())
	command := "printf 1234567890"
	if runtime.GOOS == "windows" {
		command = "Write-Output 1234567890"
	}
	result, err := w.RunShell(context.Background(), command, 5*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(result.Stdout) > 5 {
		t.Fatalf("expected bounded output: %+v", result)
	}
}

func TestShellHonorsTimeout(t *testing.T) {
	w, _ := NewWorkspace(t.TempDir())
	command := "sleep 5"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 5"
	}
	_, err := w.RunShell(context.Background(), command, 20*time.Millisecond, 1024)
	if err == nil {
		t.Fatal("expected timeout")
	}
}
