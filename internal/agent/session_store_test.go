package agent

import (
	"fmt"
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

func TestSessionStoreRecoversInterruptedSessions(t *testing.T) {
	store, _ := NewSessionStore(t.TempDir())
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	active := []SessionState{SessionStateRunning, SessionStateEditing, SessionStateDiffDetected, SessionStateVerifying, SessionStateVerifyFailed, SessionStateRepairing, SessionStateReverifying}
	for i, state := range active {
		s := testSessionSnapshot(t.TempDir())
		s.ID = fmt.Sprintf("active-%d", i)
		s.State = state
		if err := store.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	for i, state := range []SessionState{SessionStateVerified, SessionStateFailed, SessionStateCancelled} {
		s := testSessionSnapshot(t.TempDir())
		s.ID = fmt.Sprintf("terminal-%d", i)
		s.State = state
		if err := store.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecoverInterruptedSessions(now); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverInterruptedSessions(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := range active {
		s, _ := store.Load(fmt.Sprintf("active-%d", i))
		if s.State != SessionStateInterrupted || !s.UpdatedAt.Equal(now) {
			t.Fatalf("active %d = %+v", i, s)
		}
		events, _ := store.Events(s.ID)
		if len(events) != 1 || events[0].Type != EventSessionInterrupted {
			t.Fatalf("events = %#v", events)
		}
	}
	for i, want := range []SessionState{SessionStateVerified, SessionStateFailed, SessionStateCancelled} {
		s, _ := store.Load(fmt.Sprintf("terminal-%d", i))
		events, _ := store.Events(s.ID)
		if s.State != want || len(events) != 0 {
			t.Fatalf("terminal = %+v events=%#v", s, events)
		}
	}
}

func TestSessionStoreRecoveryPreservesEvidence(t *testing.T) {
	store, _ := NewSessionStore(t.TempDir())
	s := testSessionSnapshot(t.TempDir())
	s.ChangedFiles = []string{"main.go"}
	s.VerificationResults = []VerificationResult{{Command: "go test", Passed: false}}
	s.FinalSummary = "partial"
	if err := store.Save(s); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDiff(s.ID, "existing diff"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(s.ID, SessionEvent{Type: EventEditingStarted, Timestamp: s.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverInterruptedSessions(s.CreatedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Load(s.ID)
	diff, _ := store.LoadDiff(s.ID)
	events, _ := store.Events(s.ID)
	if len(got.ChangedFiles) != 1 || len(got.VerificationResults) != 1 || got.FinalSummary != "partial" || diff != "existing diff" || len(events) != 2 {
		t.Fatalf("evidence lost: %+v %q %#v", got, diff, events)
	}
}

func TestSessionStoreRecoveryRetriesSaveWithoutDuplicatingInterruptionEvent(t *testing.T) {
	store, err := NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	snapshot := testSessionSnapshot(t.TempDir())
	recoveredAt := snapshot.CreatedAt.Add(time.Hour)
	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.AppendEvent(snapshot.ID, SessionEvent{
		Type:      EventSessionInterrupted,
		Timestamp: recoveredAt,
		Message:   "session interrupted before completion",
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	if err := store.RecoverInterruptedSessions(recoveredAt); err != nil {
		t.Fatalf("RecoverInterruptedSessions: %v", err)
	}

	got, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.State != SessionStateInterrupted {
		t.Fatalf("State = %q, want %q", got.State, SessionStateInterrupted)
	}
	events, err := store.Events(snapshot.ID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Type == EventSessionInterrupted {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("session_interrupted count = %d, want 1; events=%#v", count, events)
	}
}

func TestSessionStoreRecoveryRecordsSeparateInterruptionEpisodes(t *testing.T) {
	store, err := NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	snapshot := testSessionSnapshot(t.TempDir())
	firstRecovery := snapshot.UpdatedAt.Add(time.Hour)
	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.RecoverInterruptedSessions(firstRecovery); err != nil {
		t.Fatalf("first recovery: %v", err)
	}

	continuedAt := firstRecovery.Add(time.Hour)
	snapshot, err = store.Load(snapshot.ID)
	if err != nil {
		t.Fatalf("Load after first recovery: %v", err)
	}
	snapshot.State = SessionStateRunning
	snapshot.UpdatedAt = continuedAt
	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save continued session: %v", err)
	}
	if err := store.AppendEvent(snapshot.ID, SessionEvent{
		Type:      EventSessionContinued,
		Timestamp: continuedAt,
		Message:   "session continued",
	}); err != nil {
		t.Fatalf("AppendEvent continued: %v", err)
	}

	secondRecovery := continuedAt.Add(time.Hour)
	if err := store.RecoverInterruptedSessions(secondRecovery); err != nil {
		t.Fatalf("second recovery: %v", err)
	}
	snapshot, err = store.Load(snapshot.ID)
	if err != nil {
		t.Fatalf("Load after second recovery: %v", err)
	}
	if snapshot.State != SessionStateInterrupted {
		t.Fatalf("State = %q, want %q", snapshot.State, SessionStateInterrupted)
	}
	events, err := store.Events(snapshot.ID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Type == EventSessionInterrupted {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("session_interrupted count = %d, want 2; events=%#v", count, events)
	}
}
