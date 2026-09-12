package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testSessionSnapshot(workspace string) SessionSnapshot {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)

	return SessionSnapshot{
		ID:        "session-test-001",
		Model:     "qwen3:4b-instruct",
		Workspace: workspace,
		Task:      "change a source file",
		State:     SessionStateRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestSessionStoreSaveLoadSurvivesFreshStore(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()

	store, err := NewSessionStore(root)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	want := testSessionSnapshot(workspace)

	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Prove persistence is not dependent on in-memory state.
	freshStore, err := NewSessionStore(root)
	if err != nil {
		t.Fatalf("fresh NewSessionStore: %v", err)
	}

	got, err := freshStore.Load(want.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.ID != want.ID {
		t.Fatalf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.Model != want.Model {
		t.Fatalf("Model = %q, want %q", got.Model, want.Model)
	}
	if got.Workspace != want.Workspace {
		t.Fatalf("Workspace = %q, want %q", got.Workspace, want.Workspace)
	}
	if got.Task != want.Task {
		t.Fatalf("Task = %q, want %q", got.Task, want.Task)
	}
	if got.State != want.State {
		t.Fatalf("State = %q, want %q", got.State, want.State)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
}

func TestSessionStoreSaveIsAtomicAndLeavesNoTempFile(t *testing.T) {
	root := t.TempDir()
	store, err := NewSessionStore(root)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	snapshot := testSessionSnapshot(t.TempDir())

	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sessionDir := filepath.Join(root, snapshot.ID)

	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	foundSession := false
	for _, entry := range entries {
		switch entry.Name() {
		case "session.json":
			foundSession = true
		case "session.json.tmp":
			t.Fatalf("atomic save left temporary file behind")
		}
	}

	if !foundSession {
		t.Fatalf("session.json was not created")
	}
}

func TestSessionStoreRejectsCorruptSession(t *testing.T) {
	root := t.TempDir()

	store, err := NewSessionStore(root)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	sessionID := "corrupt-session"
	sessionDir := filepath.Join(root, sessionID)

	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(sessionDir, "session.json"),
		[]byte("{ definitely not valid json"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := store.Load(sessionID); err == nil {
		t.Fatalf("Load accepted corrupt session.json")
	}
}

func TestSessionStoreEventsPreserveAppendOrder(t *testing.T) {
	root := t.TempDir()

	store, err := NewSessionStore(root)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	snapshot := testSessionSnapshot(t.TempDir())

	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save: %v", err)
	}

	first := SessionEvent{
		Type:      EventSessionCreated,
		Timestamp: time.Date(2026, 9, 13, 0, 0, 1, 0, time.UTC),
		Message:   "created",
	}

	second := SessionEvent{
		Type:      EventContextBuilt,
		Timestamp: time.Date(2026, 9, 13, 0, 0, 2, 0, time.UTC),
		Message:   "context built",
	}

	if err := store.AppendEvent(snapshot.ID, first); err != nil {
		t.Fatalf("AppendEvent first: %v", err)
	}

	if err := store.AppendEvent(snapshot.ID, second); err != nil {
		t.Fatalf("AppendEvent second: %v", err)
	}

	events, err := store.Events(snapshot.ID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}

	if events[0].Type != EventSessionCreated {
		t.Fatalf("events[0].Type = %q", events[0].Type)
	}

	if events[1].Type != EventContextBuilt {
		t.Fatalf("events[1].Type = %q", events[1].Type)
	}
}

func TestSessionStoreDiffSurvivesFreshStore(t *testing.T) {
	root := t.TempDir()

	store, err := NewSessionStore(root)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	snapshot := testSessionSnapshot(t.TempDir())

	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save: %v", err)
	}

	want := `diff --git a/main.py b/main.py
--- a/main.py
+++ b/main.py
@@ -1 +1 @@
-old
+new
`

	if err := store.SaveDiff(snapshot.ID, want); err != nil {
		t.Fatalf("SaveDiff: %v", err)
	}

	freshStore, err := NewSessionStore(root)
	if err != nil {
		t.Fatalf("fresh NewSessionStore: %v", err)
	}

	got, err := freshStore.LoadDiff(snapshot.ID)
	if err != nil {
		t.Fatalf("LoadDiff: %v", err)
	}

	if got != want {
		t.Fatalf("diff mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestSessionStoreDoesNotCopyWorkspaceSource(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()

	sourcePath := filepath.Join(workspace, "main.py")
	if err := os.WriteFile(sourcePath, []byte(`print("source")`), 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	store, err := NewSessionStore(root)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	snapshot := testSessionSnapshot(workspace)

	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sessionDir := filepath.Join(root, snapshot.ID)

	if _, err := os.Stat(filepath.Join(sessionDir, "main.py")); !os.IsNotExist(err) {
		t.Fatalf("workspace source was copied into session storage")
	}

	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("original source disappeared: %v", err)
	}
}
