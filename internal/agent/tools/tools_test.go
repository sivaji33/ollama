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

func TestWorkspaceAllowsPathsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	w, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := w.Resolve(filepath.Join(root, "..", "outside.txt"), false)
	if err != nil {
		t.Fatalf("outside absolute path: %v", err)
	}
	traversal, err := w.Resolve(filepath.Join("..", "outside.txt"), false)
	if err != nil {
		t.Fatalf("escaping traversal: %v", err)
	}
	if absolute != traversal {
		t.Fatalf("absolute resolved = %q, traversal resolved = %q", absolute, traversal)
	}
	if rel, relErr := filepath.Rel(root, absolute); relErr != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("resolved %q is not outside %q", absolute, root)
	}
	if got := filepath.Base(absolute); got != "outside.txt" {
		t.Fatalf("base = %q", got)
	}
}

func TestWorkspaceResolvesSymlinksOutsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires Windows privilege")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	resolved, err := w.Resolve("link/file.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(want, "file.txt") {
		t.Fatalf("resolved = %q, want %q", resolved, filepath.Join(want, "file.txt"))
	}
}

func TestReadFileAllowsAbsolutePathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outsideFile := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outsideFile, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	got, err := w.ReadFile(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if got != "package outside\n" {
		t.Fatalf("got %q", got)
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

func TestShellAllowsPathsAndDirectoryChangesOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside-content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWorkspace(root)
	rel, err := filepath.Rel(root, outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	readAbsolute, readTraversal := `cat "`+outsideFile+`"`, `cat "`+filepath.ToSlash(rel)+`"`
	if runtime.GOOS == "windows" {
		readAbsolute, readTraversal = `Get-Content "`+outsideFile+`"`, `Get-Content "`+filepath.ToSlash(rel)+`"`
	}
	for _, command := range []string{readAbsolute, readTraversal} {
		result, err := w.RunShell(context.Background(), command, 10*time.Second, 4096)
		if err != nil {
			t.Fatalf("%q: %v", command, err)
		}
		if result.ExitCode != 0 || !strings.Contains(result.Stdout, "outside-content") {
			t.Fatalf("%q: %+v", command, result)
		}
	}
	cdCommand := "cd .. && pwd"
	if runtime.GOOS == "windows" {
		cdCommand = "cd ..; (Get-Location).Path"
	}
	result, err := w.RunShell(context.Background(), cdCommand, 10*time.Second, 4096)
	if err != nil {
		t.Fatal(err)
	}
	wantParent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(strings.TrimSpace(result.Stdout), wantParent) {
		t.Fatalf("%q ran in %q, want %q", cdCommand, strings.TrimSpace(result.Stdout), wantParent)
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
