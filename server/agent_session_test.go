package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	agentpkg "github.com/ollama/ollama/internal/agent"
)

type sessionAPITestRunner struct {
	mu      sync.Mutex
	request agentpkg.RunRequest
	result  agentpkg.RunResult
	err     error
}

func (r *sessionAPITestRunner) Run(
	_ context.Context,
	request agentpkg.RunRequest,
) (agentpkg.RunResult, error) {
	r.mu.Lock()
	r.request = request
	r.mu.Unlock()
	return r.result, r.err
}

func (r *sessionAPITestRunner) lastRequest() agentpkg.RunRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.request
}

func newSessionAPITestServer(t *testing.T, runner agentRunner) (*Server, *agentpkg.SessionStore) {
	t.Helper()

	store, err := agentpkg.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	server := &Server{
		agentRunner:       runner,
		agentSessionStore: store,
	}

	return server, store
}

func sessionAPIRouter(server *Server) http.Handler {
	gin.SetMode(gin.TestMode)

	router := gin.New()

	router.POST("/api/agent/session", server.AgentSessionCreateHandler)
	router.GET("/api/agent/session/:id", server.AgentSessionGetHandler)
	router.GET("/api/agent/session/:id/events", server.AgentSessionEventsHandler)
	router.GET("/api/agent/session/:id/diff", server.AgentSessionDiffHandler)

	return router
}

func TestAgentSessionCreateUsesDefaultAgentModelAndPersistsResult(t *testing.T) {
	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			SessionID:     "session-http-001",
			Status:        agentpkg.StatusSuccess,
			StepsExecuted: 4,
			ChangedFiles:  []string{"main.py"},
			GitDiff:       "diff --git a/main.py b/main.py\n",
			FinalSummary:  "verified change",
		},
	}

	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	body := []byte(`{
        "workspace":"C:\\repo",
        "task":"change main.py",
        "verify":["python -m pytest"]
    }`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}

	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted session: %v", err)
	}
	if accepted.State != agentpkg.SessionStateRunning {
		t.Fatalf("accepted state = %q, want %q", accepted.State, agentpkg.SessionStateRunning)
	}

	snapshot := waitForPersistedSessionState(
		t,
		store,
		accepted.ID,
		agentpkg.SessionStateVerified,
	)
	runRequest := runner.lastRequest()
	if runRequest.Model != agentpkg.DefaultAgentModel {
		t.Fatalf(
			"runner model = %q, want %q",
			runRequest.Model,
			agentpkg.DefaultAgentModel,
		)
	}

	if snapshot.Model != agentpkg.DefaultAgentModel {
		t.Fatalf(
			"snapshot model = %q, want %q",
			snapshot.Model,
			agentpkg.DefaultAgentModel,
		)
	}

	if snapshot.State != agentpkg.SessionStateVerified {
		t.Fatalf(
			"snapshot state = %q, want %q",
			snapshot.State,
			agentpkg.SessionStateVerified,
		)
	}

	if snapshot.StepsExecuted != 4 {
		t.Fatalf(
			"StepsExecuted = %d, want 4",
			snapshot.StepsExecuted,
		)
	}

	diff, err := store.LoadDiff(snapshot.ID)
	if err != nil {
		t.Fatalf("LoadDiff: %v", err)
	}

	if diff != runner.result.GitDiff {
		t.Fatalf("persisted diff = %q", diff)
	}

	events, err := store.Events(snapshot.ID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	if len(events) < 2 {
		t.Fatalf("events = %#v, want create and completion events", events)
	}

	if events[0].Type != agentpkg.EventSessionCreated {
		t.Fatalf(
			"first event = %q, want %q",
			events[0].Type,
			agentpkg.EventSessionCreated,
		)
	}

	if events[len(events)-1].Type != agentpkg.EventCompleted {
		t.Fatalf(
			"last event = %q, want %q",
			events[len(events)-1].Type,
			agentpkg.EventCompleted,
		)
	}
}

func TestAgentSessionCreateHonorsExplicitModel(t *testing.T) {
	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			SessionID:    "session-model-override",
			Status:       agentpkg.StatusFailed,
			FinalSummary: "test failure",
		},
	}

	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	body := []byte(`{
        "model":"custom-model:latest",
        "workspace":"C:\\repo",
        "task":"inspect project"
    }`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}

	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted session: %v", err)
	}
	waitForPersistedSessionState(
		t,
		store,
		accepted.ID,
		agentpkg.SessionStateFailed,
	)
	runRequest := runner.lastRequest()
	if runRequest.Model != "custom-model:latest" {
		t.Fatalf(
			"runner model = %q, want custom-model:latest",
			runRequest.Model,
		)
	}
}

func TestAgentSessionReadEndpointsSurviveFreshStore(t *testing.T) {
	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			SessionID:     "session-read-001",
			Status:        agentpkg.StatusSuccess,
			StepsExecuted: 3,
			ChangedFiles:  []string{"main.py"},
			GitDiff:       "example-diff",
			FinalSummary:  "done",
		},
	}

	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	createBody := []byte(`{
        "workspace":"C:\\repo",
        "task":"change main.py"
    }`)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session",
		bytes.NewReader(createBody),
	)
	createReq.Header.Set("Content-Type", "application/json")

	createRecorder := httptest.NewRecorder()
	router.ServeHTTP(createRecorder, createReq)

	if createRecorder.Code != http.StatusOK {
		t.Fatalf("create status = %d", createRecorder.Code)
	}

	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted session: %v", err)
	}
	waitForPersistedSessionState(
		t,
		store,
		accepted.ID,
		agentpkg.SessionStateVerified,
	)

	// Simulate process-level recreation by using a fresh SessionStore object
	// pointed at the same persistent root.
	freshStore, err := agentpkg.NewSessionStore(store.Root())
	if err != nil {
		t.Fatalf("fresh NewSessionStore: %v", err)
	}

	server.agentSessionStore = freshStore

	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		getRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/agent/session/"+accepted.ID,
			nil,
		),
	)

	if getRecorder.Code != http.StatusOK {
		t.Fatalf(
			"GET session status = %d body=%s",
			getRecorder.Code,
			getRecorder.Body.String(),
		)
	}

	var snapshot agentpkg.SessionSnapshot
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if snapshot.ID != accepted.ID {
		t.Fatalf("snapshot ID = %q", snapshot.ID)
	}

	eventsRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		eventsRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/agent/session/"+accepted.ID+"/events",
			nil,
		),
	)

	if eventsRecorder.Code != http.StatusOK {
		t.Fatalf(
			"GET events status = %d body=%s",
			eventsRecorder.Code,
			eventsRecorder.Body.String(),
		)
	}

	diffRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		diffRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/agent/session/"+accepted.ID+"/diff",
			nil,
		),
	)

	if diffRecorder.Code != http.StatusOK {
		t.Fatalf(
			"GET diff status = %d body=%s",
			diffRecorder.Code,
			diffRecorder.Body.String(),
		)
	}

	var diffPayload struct {
		Diff string `json:"diff"`
	}

	if err := json.Unmarshal(diffRecorder.Body.Bytes(), &diffPayload); err != nil {
		t.Fatalf("decode diff: %v", err)
	}

	if diffPayload.Diff != "example-diff" {
		t.Fatalf("diff = %q, want example-diff", diffPayload.Diff)
	}
}

func TestAgentRunHandlerRemainsBackwardCompatible(t *testing.T) {
	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			SessionID:    "legacy-run",
			Status:       agentpkg.StatusSuccess,
			FinalSummary: "legacy endpoint works",
		},
	}

	server := &Server{agentRunner: runner}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/agent/run", server.AgentRunHandler)

	body := []byte(`{
        "model":"qwen3:4b-instruct",
        "workspace":"C:\\repo",
        "task":"legacy request"
    }`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/run",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"legacy status = %d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}

	legacyRequest := runner.lastRequest()
	if legacyRequest.Task != "legacy request" {
		t.Fatalf("legacy runner task = %q", legacyRequest.Task)
	}
}
