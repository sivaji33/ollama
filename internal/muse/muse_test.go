package muse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPolicySafety(t *testing.T) {
	for _, action := range []Action{ActionReadFile, ActionListFiles, ActionSearchFiles, ActionGitStatus, ActionGitDiff, ActionWebFetch} {
		if PolicyFor(action).RequireApproval {
			t.Fatalf("%s should be automatic", action)
		}
	}
	d := PolicyFor(ActionDownload)
	if !d.RequireApproval || d.TimeoutSeconds != 5 || d.OnTimeout != TimeoutAllow {
		t.Fatalf("download policy = %+v", d)
	}
	for _, action := range []Action{ActionWriteFile, ActionDeleteFile, ActionShell, ActionGitPush, ActionBrowser, ActionConnector, ActionPayment, ActionPromote} {
		p := PolicyFor(action)
		if !p.RequireApproval || p.OnTimeout != TimeoutDeny {
			t.Fatalf("%s must fail closed: %+v", action, p)
		}
	}
}

func TestPathGuardAndFilesystem(t *testing.T) {
	root := t.TempDir()
	guard, err := NewPathGuard(root)
	if err != nil {
		t.Fatal(err)
	}
	fs := &FileSystem{Guard: guard}
	path := filepath.Join(root, "a.txt")
	if _, err := fs.Write(path, "hello"); err != nil {
		t.Fatal(err)
	}
	got, err := fs.Read(path)
	if err != nil || got != "hello" {
		t.Fatalf("read=%q err=%v", got, err)
	}
	if _, err := guard.Resolve(filepath.Join(filepath.Dir(root), "outside.txt")); err == nil {
		t.Fatal("outside path should be blocked")
	}
}

func TestApprovalFailsClosed(t *testing.T) {
	store := NewApprovalStore()
	req := Request{SessionID: "s", Action: ActionWriteFile, Target: "x"}
	item := store.Request(req)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := store.Wait(ctx, item, PolicyFor(ActionWriteFile)); err == nil {
		t.Fatal("missing approval must fail closed")
	}
}

func TestMemoryRoundTrip(t *testing.T) {
	m := &Memory{Path: filepath.Join(t.TempDir(), "memory.jsonl")}
	if _, err := m.Remember("lesson", "use generate endpoint", "test", "planner"); err != nil {
		t.Fatal(err)
	}
	got, err := m.Recall("generate", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestCheckpointRoundTrip(t *testing.T) {
	root := t.TempDir()
	guard, _ := NewPathGuard(root)
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &CheckpointStore{Root: filepath.Join(root, ".muse", "checkpoints"), Guard: guard}
	rec, err := store.Save(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore(rec); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "one" {
		t.Fatalf("restore=%q", b)
	}
}
