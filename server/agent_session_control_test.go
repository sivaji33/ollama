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

func TestAgentSessionContinueReusesPersistentSessionIdentity(t *testing.T) {
	store, err := agentpkg.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	now := time.Now().UTC()

	original := agentpkg.SessionSnapshot{
		ID:        "session-continue-001",
		Model:     agentpkg.DefaultAgentModel,
		Workspace: `C:\repo`,
		Task:      "original task",
		State:     agentpkg.SessionStateVerified,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save initial session: %v", err)
	}

	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			// Handler must preserve the existing persistent session identity
			// even if a runner attempts to return another ID.
			SessionID:     "different-session-id",
			Status:        agentpkg.StatusSuccess,
			StepsExecuted: 2,
			ChangedFiles:  []string{"main.py"},
			GitDiff:       "continued-diff",
			FinalSummary:  "continuation verified",
		},
	}

	server := &Server{
		agentRunner:       runner,
		agentSessionStore: store,
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(
		"/api/agent/session/:id/continue",
		server.AgentSessionContinueHandler,
	)

	body := []byte(`{
        "task":"finish the repair",
        "verify":["go test ./..."]
    }`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session/session-continue-001/continue",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}

	if runner.request.SessionID != original.ID {
		t.Fatalf(
			"runner SessionID = %q, want %q",
			runner.request.SessionID,
			original.ID,
		)
	}

	if runner.request.Model != original.Model {
		t.Fatalf(
			"runner Model = %q, want %q",
			runner.request.Model,
			original.Model,
		)
	}

	if runner.request.Workspace != original.Workspace {
		t.Fatalf(
			"runner Workspace = %q, want %q",
			runner.request.Workspace,
			original.Workspace,
		)
	}

	if runner.request.Task != "finish the repair" {
		t.Fatalf(
			"runner Task = %q, want finish the repair",
			runner.request.Task,
		)
	}

	updated, err := store.Load(original.ID)
	if err != nil {
		t.Fatalf("Load continued session: %v", err)
	}

	if updated.ID != original.ID {
		t.Fatalf(
			"updated ID = %q, want %q",
			updated.ID,
			original.ID,
		)
	}

	if updated.State != agentpkg.SessionStateVerified {
		t.Fatalf(
			"updated state = %q, want %q",
			updated.State,
			agentpkg.SessionStateVerified,
		)
	}

	if updated.Task != "finish the repair" {
		t.Fatalf(
			"updated task = %q",
			updated.Task,
		)
	}

	if _, err := store.Load("different-session-id"); err == nil {
		t.Fatalf("continuation incorrectly created a second session")
	}

	events, err := store.Events(original.ID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	foundContinuation := false

	for _, event := range events {
		if event.Type == agentpkg.EventSessionContinued {
			foundContinuation = true
			break
		}
	}

	if !foundContinuation {
		t.Fatalf(
			"events = %#v, want %q",
			events,
			agentpkg.EventSessionContinued,
		)
	}
}

type cancellableSessionRunner struct {
	started  chan agentpkg.RunRequest
	returned chan struct{}
}

func (r *cancellableSessionRunner) Run(
	ctx context.Context,
	request agentpkg.RunRequest,
) (agentpkg.RunResult, error) {
	r.started <- request

	<-ctx.Done()

	close(r.returned)

	// Deliberately return "success" after cancellation.
	// The persisted CANCELLED state must still win.
	return agentpkg.RunResult{
		SessionID:     request.SessionID,
		Status:        agentpkg.StatusSuccess,
		StepsExecuted: 5,
		ChangedFiles:  []string{"main.py"},
		GitDiff:       "late-success-diff",
		FinalSummary:  "late success must not overwrite cancellation",
	}, nil
}

func TestAgentSessionCancelStopsActiveRunAndCancellationWins(t *testing.T) {
	store, err := agentpkg.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	runner := &cancellableSessionRunner{
		started:  make(chan agentpkg.RunRequest, 1),
		returned: make(chan struct{}),
	}

	server := &Server{
		agentRunner:       runner,
		agentSessionStore: store,
	}

	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.POST(
		"/api/agent/session",
		server.AgentSessionCreateHandler,
	)
	router.POST(
		"/api/agent/session/:id/cancel",
		server.AgentSessionCancelHandler,
	)

	createBody := []byte(`{
        "workspace":"C:\\repo",
        "task":"long running change"
    }`)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session",
		bytes.NewReader(createBody),
	)
	createReq.Header.Set("Content-Type", "application/json")

	createRecorder := httptest.NewRecorder()
	createDone := make(chan struct{})

	go func() {
		defer close(createDone)
		router.ServeHTTP(createRecorder, createReq)
	}()

	var startedRequest agentpkg.RunRequest

	select {
	case startedRequest = <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}

	if startedRequest.SessionID == "" {
		t.Fatalf("runner started without a session ID")
	}

	runningSnapshot, err := store.Load(startedRequest.SessionID)
	if err != nil {
		t.Fatalf(
			"active session was not persisted before Run: %v",
			err,
		)
	}

	if runningSnapshot.State != agentpkg.SessionStateRunning {
		t.Fatalf(
			"active state = %q, want %q",
			runningSnapshot.State,
			agentpkg.SessionStateRunning,
		)
	}

	cancelReq := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session/"+startedRequest.SessionID+"/cancel",
		nil,
	)

	cancelRecorder := httptest.NewRecorder()
	router.ServeHTTP(cancelRecorder, cancelReq)

	if cancelRecorder.Code != http.StatusOK {
		t.Fatalf(
			"cancel status = %d body=%s",
			cancelRecorder.Code,
			cancelRecorder.Body.String(),
		)
	}

	select {
	case <-runner.returned:
	case <-time.After(5 * time.Second):
		t.Fatal("runner was not cancelled")
	}

	select {
	case <-createDone:
	case <-time.After(5 * time.Second):
		t.Fatal("create handler did not finish after cancellation")
	}

	finalSnapshot, err := store.Load(startedRequest.SessionID)
	if err != nil {
		t.Fatalf("Load final session: %v", err)
	}

	if finalSnapshot.State != agentpkg.SessionStateCancelled {
		t.Fatalf(
			"final state = %q, want %q",
			finalSnapshot.State,
			agentpkg.SessionStateCancelled,
		)
	}

	if finalSnapshot.State == agentpkg.SessionStateVerified {
		t.Fatal("cancelled session became VERIFIED")
	}

	events, err := store.Events(startedRequest.SessionID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	foundRequested := false
	foundCancelled := false

	for _, event := range events {
		switch event.Type {
		case agentpkg.EventCancelRequested:
			foundRequested = true
		case agentpkg.EventCancelled:
			foundCancelled = true
		}
	}

	if !foundRequested {
		t.Fatalf(
			"events = %#v, missing %q",
			events,
			agentpkg.EventCancelRequested,
		)
	}

	if !foundCancelled {
		t.Fatalf(
			"events = %#v, missing %q",
			events,
			agentpkg.EventCancelled,
		)
	}
}
