package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	agentpkg "github.com/ollama/ollama/internal/agent"
)

type recoveryCountingStore struct {
	*agentpkg.SessionStore
	calls int
}

func (s *recoveryCountingStore) RecoverInterruptedSessions(at time.Time) error {
	s.calls++
	return s.SessionStore.RecoverInterruptedSessions(at)
}

func TestAgentSessionFreshServerRecoversPersistedActiveSessionOnce(t *testing.T) {
	store, err := agentpkg.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := agentpkg.SessionSnapshot{ID: "stale-session", Model: "test", Workspace: "C:/repo", Task: "edit", State: agentpkg.SessionStateVerifying, CreatedAt: now, UpdatedAt: now}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(snapshot.ID, agentpkg.SessionEvent{Type: agentpkg.EventVerificationStart, Timestamp: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDiff(snapshot.ID, "old diff"); err != nil {
		t.Fatal(err)
	}

	countingStore := &recoveryCountingStore{SessionStore: store}
	server := &Server{agentSessionStore: countingStore}
	router := sessionAPIRouter(server)
	for attempt := 0; attempt < 2; attempt++ {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/agent/session/"+snapshot.ID, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %d status=%d body=%s", attempt, recorder.Code, recorder.Body.String())
		}
		got, _ := store.Load(snapshot.ID)
		if got.State != agentpkg.SessionStateInterrupted {
			t.Fatalf("GET %d state=%q, want INTERRUPTED", attempt, got.State)
		}
	}
	events, _ := store.Events(snapshot.ID)
	interrupted := 0
	for _, event := range events {
		switch event.Type {
		case agentpkg.EventSessionInterrupted:
			interrupted++
		case agentpkg.EventVerificationPassed, agentpkg.EventCompleted:
			t.Fatalf("recovery fabricated completion: %#v", events)
		}
	}
	if interrupted != 1 {
		t.Fatalf("session_interrupted count=%d, want 1", interrupted)
	}
	if countingStore.calls != 1 {
		t.Fatalf("recovery calls=%d, want 1", countingStore.calls)
	}
	if diff, _ := store.LoadDiff(snapshot.ID); diff != "old diff" {
		t.Fatalf("diff=%q, want old diff", diff)
	}
}

type interruptedContinuationRunner struct {
	started chan agentpkg.RunRequest
	release chan struct{}
}

func (r *interruptedContinuationRunner) Run(_ context.Context, request agentpkg.RunRequest) (agentpkg.RunResult, error) {
	r.started <- request
	<-r.release
	return agentpkg.RunResult{Status: agentpkg.StatusFailed, FinalSummary: "no new diff"}, nil
}

func TestAgentSessionContinuesInterruptedSessionWithoutReplacingOldDiff(t *testing.T) {
	store, _ := agentpkg.NewSessionStore(t.TempDir())
	now := time.Now().UTC()
	snapshot := agentpkg.SessionSnapshot{ID: "interrupted-session", Model: "test-model", Workspace: "C:/repo", Task: "old task", State: agentpkg.SessionStateInterrupted, CreatedAt: now, UpdatedAt: now}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(snapshot.ID, agentpkg.SessionEvent{Type: agentpkg.EventSessionInterrupted, Timestamp: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDiff(snapshot.ID, "old diff"); err != nil {
		t.Fatal(err)
	}
	runner := &interruptedContinuationRunner{started: make(chan agentpkg.RunRequest, 1), release: make(chan struct{})}
	server := &Server{agentRunner: runner, agentSessionStore: store}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/agent/session/:id/continue", server.AgentSessionContinueHandler)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/api/agent/session/"+snapshot.ID+"/continue", bytes.NewReader([]byte(`{"task":"continued task"}`)))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
	}()
	request := <-runner.started
	if request.SessionID != snapshot.ID || request.Model != snapshot.Model || request.Workspace != snapshot.Workspace {
		t.Fatalf("request identity changed: %+v", request)
	}
	if request.OnLifecycleEvent == nil {
		t.Fatal("OnLifecycleEvent is nil")
	}
	active, _ := store.Load(snapshot.ID)
	if active.State != agentpkg.SessionStateRunning {
		t.Fatalf("active state=%q, want RUNNING", active.State)
	}
	if diff, _ := store.LoadDiff(snapshot.ID); diff != "old diff" {
		t.Fatalf("active diff=%q, want old diff", diff)
	}
	close(runner.release)
	<-done
	waitForPersistedSessionState(
		t,
		store,
		snapshot.ID,
		agentpkg.SessionStateFailed,
	)
	if diff, _ := store.LoadDiff(snapshot.ID); diff != "old diff" {
		t.Fatalf("final diff=%q, want old diff", diff)
	}
	events, _ := store.Events(snapshot.ID)
	if events[0].Type != agentpkg.EventSessionInterrupted {
		t.Fatalf("history replaced: %#v", events)
	}
}
