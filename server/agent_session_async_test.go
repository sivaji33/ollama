package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	agentpkg "github.com/ollama/ollama/internal/agent"
)

type blockingAsyncSessionRunner struct {
	started  chan agentpkg.RunRequest
	release  chan struct{}
	finished chan error
}

func (r *blockingAsyncSessionRunner) Run(
	ctx context.Context,
	request agentpkg.RunRequest,
) (agentpkg.RunResult, error) {
	r.started <- request

	select {
	case <-r.release:
		r.finished <- nil
		return agentpkg.RunResult{
			SessionID:    request.SessionID,
			Status:       agentpkg.StatusSuccess,
			FinalSummary: "verified",
		}, nil
	case <-ctx.Done():
		r.finished <- ctx.Err()
		return agentpkg.RunResult{
			SessionID:    request.SessionID,
			Status:       agentpkg.StatusFailed,
			FinalSummary: ctx.Err().Error(),
		}, ctx.Err()
	}
}

func waitForPersistedSessionState(
	t *testing.T,
	store *agentpkg.SessionStore,
	sessionID string,
	want agentpkg.SessionState,
) agentpkg.SessionSnapshot {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := store.Load(sessionID)
		if err == nil && snapshot.State == want {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}

	snapshot, err := store.Load(sessionID)
	if err != nil {
		t.Fatalf("Load(%q): %v", sessionID, err)
	}
	t.Fatalf("session %q state = %q, want %q", sessionID, snapshot.State, want)
	return agentpkg.SessionSnapshot{}
}

func waitForPersistedSessionEvent(
	t *testing.T,
	store *agentpkg.SessionStore,
	sessionID string,
	want agentpkg.SessionEventType,
) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		events, err := store.Events(sessionID)
		if err == nil {
			for _, event := range events {
				if event.Type == want {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	events, err := store.Events(sessionID)
	if err != nil {
		t.Fatalf("Events(%q): %v", sessionID, err)
	}
	t.Fatalf("session %q events do not contain %q: %#v", sessionID, want, events)
}

func TestAgentSessionCreateReturnsBeforeRunnerCompletesAndOutlivesHTTPRequest(t *testing.T) {
	runner := &blockingAsyncSessionRunner{
		started:  make(chan agentpkg.RunRequest, 1),
		release:  make(chan struct{}),
		finished: make(chan error, 1),
	}
	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session",
		bytes.NewReader([]byte(`{
            "workspace":"C:/repo",
            "task":"long running change"
        }`)),
	).WithContext(requestContext)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handlerDone := make(chan struct{})
	go func() {
		router.ServeHTTP(recorder, req)
		close(handlerDone)
	}()

	select {
	case <-handlerDone:
	case <-time.After(500 * time.Millisecond):
		close(runner.release)
		t.Fatal("create handler waited for the long-running agent")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted session: %v", err)
	}
	if accepted.ID == "" {
		t.Fatal("accepted session ID is empty")
	}
	if accepted.State != agentpkg.SessionStateRunning {
		t.Fatalf("accepted state = %q, want %q", accepted.State, agentpkg.SessionStateRunning)
	}

	var started agentpkg.RunRequest
	select {
	case started = <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background runner did not start")
	}
	if started.SessionID != accepted.ID {
		t.Fatalf("runner session ID = %q, accepted ID = %q", started.SessionID, accepted.ID)
	}

	// Simulate the HTTP request lifecycle ending after the response. The
	// persistent coding run must keep going until explicit cancellation or
	// normal completion.
	cancelRequest()
	select {
	case err := <-runner.finished:
		t.Fatalf("background run ended with HTTP request context: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(runner.release)
	select {
	case err := <-runner.finished:
		if err != nil {
			t.Fatalf("runner finished with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background runner did not finish")
	}

	waitForPersistedSessionState(
		t,
		store,
		accepted.ID,
		agentpkg.SessionStateVerified,
	)
}

func TestAgentSessionContinueReturnsRunningSnapshotBeforeRunnerCompletes(t *testing.T) {
	store, err := agentpkg.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	now := time.Now().UTC()
	initial := agentpkg.SessionSnapshot{
		ID:        "session-continue-async",
		Model:     agentpkg.DefaultAgentModel,
		Workspace: "C:/repo",
		Task:      "first task",
		State:     agentpkg.SessionStateVerified,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Save(initial); err != nil {
		t.Fatalf("Save initial session: %v", err)
	}

	runner := &blockingAsyncSessionRunner{
		started:  make(chan agentpkg.RunRequest, 1),
		release:  make(chan struct{}),
		finished: make(chan error, 1),
	}
	server := &Server{agentRunner: runner, agentSessionStore: store}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(
		"/api/agent/session/:id/continue",
		server.AgentSessionContinueHandler,
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session/session-continue-async/continue",
		bytes.NewReader([]byte(`{"task":"continue repair"}`)),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handlerDone := make(chan struct{})
	go func() {
		router.ServeHTTP(recorder, req)
		close(handlerDone)
	}()

	select {
	case <-handlerDone:
	case <-time.After(500 * time.Millisecond):
		close(runner.release)
		t.Fatal("continue handler waited for the long-running agent")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode continued session: %v", err)
	}
	if accepted.ID != initial.ID {
		t.Fatalf("continued ID = %q, want %q", accepted.ID, initial.ID)
	}
	if accepted.State != agentpkg.SessionStateRunning {
		t.Fatalf("continued state = %q, want %q", accepted.State, agentpkg.SessionStateRunning)
	}

	select {
	case started := <-runner.started:
		if started.SessionID != initial.ID {
			t.Fatalf("runner SessionID = %q, want %q", started.SessionID, initial.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("continued background runner did not start")
	}

	close(runner.release)
	select {
	case err := <-runner.finished:
		if err != nil {
			t.Fatalf("continued runner finished with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("continued background runner did not finish")
	}

	waitForPersistedSessionState(
		t,
		store,
		initial.ID,
		agentpkg.SessionStateVerified,
	)
}
