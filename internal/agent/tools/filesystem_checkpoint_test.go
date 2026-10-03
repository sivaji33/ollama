package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemCheckpointDiffAndRollback(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("modified.go", "package old\n")
	write("deleted.go", "package gone\n")
	write("moved.txt", "move me\n")

	checkpoint, err := NewFilesystemCheckpoint(root)
	if err != nil {
		t.Fatal(err)
	}
	defer checkpoint.Close()

	for _, name := range []string{"modified.go", "deleted.go", "moved.txt", "renamed.txt", "new/deep/file.txt"} {
		if err := checkpoint.BeforeMutation(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	write("modified.go", "package changed\n")
	if err := os.Remove(filepath.Join(root, "deleted.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "moved.txt"), filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	write("new/deep/file.txt", "created\n")

	diff, err := checkpoint.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Files) != 4 {
		t.Fatalf("diff files = %d, want 4: %+v", len(diff.Files), diff.Files)
	}
	if diff.Additions == 0 || diff.Deletions == 0 || diff.Unified == "" {
		t.Fatalf("diff lacks actual unified change details: %+v", diff)
	}
	if diff.Additions != 2 || diff.Deletions != 2 {
		t.Fatalf("diff totals = +%d/-%d, want +2/-2", diff.Additions, diff.Deletions)
	}
	if !strings.Contains(diff.Unified, "@@ -1 +1 @@\n-package old\n+package changed\n") {
		t.Fatalf("unified diff did not preserve line boundaries and hunk line numbers: %q", diff.Unified)
	}
	var sawModified, sawDeleted, sawCreated, sawMoved bool
	for _, file := range diff.Files {
		switch file.Status {
		case "modified":
			sawModified = file.Path == "modified.go" && file.Before == "package old\n" && file.After == "package changed\n"
		case "deleted":
			sawDeleted = file.Path == "deleted.go"
		case "created":
			sawCreated = file.Path == "new/deep/file.txt"
		case "moved":
			sawMoved = file.OldPath == "moved.txt" && file.Path == "renamed.txt"
		}
	}
	if !sawModified || !sawDeleted || !sawCreated || !sawMoved {
		t.Fatalf("diff did not represent all filesystem changes (modified=%t deleted=%t created=%t moved=%t): %+v", sawModified, sawDeleted, sawCreated, sawMoved, diff.Files)
	}

	restored, err := checkpoint.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 5 {
		t.Fatalf("rollback affected paths = %v, want all five paths", restored)
	}
	for name, want := range map[string]string{
		"modified.go": "package old\n",
		"deleted.go":  "package gone\n",
		"moved.txt":   "move me\n",
	} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"renamed.txt", "new/deep/file.txt", "new"} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Errorf("transaction-created path %s remains (err=%v)", name, err)
		}
	}
}

func TestFilesystemCheckpointTracksEditedMove(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "before.txt"), []byte("old line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := NewFilesystemCheckpoint(root)
	if err != nil {
		t.Fatal(err)
	}
	defer checkpoint.Close()
	workspace, err := NewRestrictedWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SetBeforeMutation(checkpoint.BeforeMutation)
	workspace.SetBeforeMove(checkpoint.BeforeMove)
	if _, err := workspace.MoveFile("before.txt", "after.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.WriteFile("after.txt", "new line\n"); err != nil {
		t.Fatal(err)
	}

	diff, err := checkpoint.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Files) != 1 {
		t.Fatalf("move diff files = %+v, want one moved file", diff.Files)
	}
	change := diff.Files[0]
	if change.Status != "moved" || change.OldPath != "before.txt" || change.Path != "after.txt" {
		t.Fatalf("move diff = %+v, want before.txt -> after.txt", change)
	}
	if change.Additions != 1 || change.Deletions != 1 || !strings.Contains(change.Unified, "-old line\n+new line\n") {
		t.Fatalf("edited move diff lost its content changes: %+v", change)
	}
	if _, err := checkpoint.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "before.txt")); err != nil || string(got) != "old line\n" {
		t.Fatalf("rollback source = %q, %v; want original content", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "after.txt")); !os.IsNotExist(err) {
		t.Fatalf("rollback left moved destination behind (err=%v)", err)
	}
}

func TestRedactFilesystemDiffRemovesSecretsFromFileAndUnifiedContent(t *testing.T) {
	diff := FilesystemDiff{
		Files: []FilesystemChange{
			{
				Path:      ".env.local",
				Status:    "modified",
				Before:    "API_TOKEN=old-secret\nKEEP=visible\n",
				After:     "API_TOKEN=new-secret\nKEEP=visible\n",
				Unified:   "diff --git a/.env.local b/.env.local\n--- a/.env.local\n+++ b/.env.local\n-API_TOKEN=old-secret\n+API_TOKEN=new-secret\n KEEP=visible\n",
				Additions: 1,
				Deletions: 1,
			},
			{
				Path:    "config.go",
				Status:  "modified",
				Before:  `password: "old-password"` + "\n",
				After:   `password: "new-password"` + "\n",
				Unified: "--- a/config.go\n+++ b/config.go\n-password: \"old-password\"\n+password: \"new-password\"\n",
			},
		},
		Unified: "untrusted stale diff",
	}

	redacted := RedactFilesystemDiff(diff)
	for _, secret := range []string{
		"old-secret",
		"new-secret",
		"old-password",
		"new-password",
		"untrusted stale diff",
	} {
		encoded, err := json.Marshal(redacted)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), secret) {
			t.Errorf("redacted diff contains secret %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(redacted.Files[0].After, "KEEP=[REDACTED]") {
		t.Fatal(".env values should be redacted")
	}
	if !strings.Contains(redacted.Files[1].Unified, "password: [REDACTED]") {
		t.Fatalf("sensitive assignment was not redacted: %q", redacted.Files[1].Unified)
	}
}

func TestRestrictedWorkspaceRejectsPathsOutsideRootAndGitMetadata(t *testing.T) {
	root := t.TempDir()
	workspace, err := NewRestrictedWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.go")
	for _, path := range []string{outside, ".git/config"} {
		if _, err := workspace.Resolve(path, false); err == nil {
			t.Errorf("Resolve(%q) unexpectedly succeeded", path)
		}
	}
}
