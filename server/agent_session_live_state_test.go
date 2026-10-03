package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/ollama/ollama/internal/agent"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

type liveSessionStateRunner struct {
	sessionID chan string
	published chan agentpkg.SessionEventType
	release   chan struct{}
}

type liveFirstPassStateRunner struct {
	sessionID chan string
	published chan agentpkg.SessionEventType
	release   chan struct{}
}

type liveFileDiffRunner struct {
	published chan struct{}
	release   chan struct{}
}

func (r *liveFileDiffRunner) Run(
	_ context.Context,
	request agentpkg.RunRequest,
) (agentpkg.RunResult, error) {
	checkpoint, err := agenttools.NewFilesystemCheckpoint(request.Workspace)
	if err != nil {
		return agentpkg.RunResult{}, err
	}
	defer checkpoint.Close()
	path := filepath.Join(request.Workspace, "live-change.go")
	if err := checkpoint.BeforeMutation(path); err != nil {
		return agentpkg.RunResult{}, err
	}
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		return agentpkg.RunResult{}, err
	}
	diff, err := checkpoint.Diff()
	if err != nil {
		return agentpkg.RunResult{}, err
	}
	safeDiff := agenttools.RedactFilesystemDiff(diff)
	event := agentpkg.SessionEvent{
		Type:           agentpkg.EventFilesystemChange,
		Timestamp:      time.Now().UTC(),
		Path:           "live-change.go",
		FilesystemDiff: &safeDiff,
	}
	result := agentpkg.RunResult{Status: agentpkg.StatusSuccess, LifecycleEvents: []agentpkg.SessionEvent{event}}
	if err := request.OnLifecycleEvent(event); err != nil {
		return result, err
	}
	close(r.published)
	<-r.release
	return result, nil
}

func (r *liveFirstPassStateRunner) Run(
	_ context.Context,
	request agentpkg.RunRequest,
) (agentpkg.RunResult, error) {
	r.sessionID <- request.SessionID
	eventTypes := []agentpkg.SessionEventType{
		agentpkg.EventEditingStarted,
		agentpkg.EventDiffDetected,
		agentpkg.EventVerificationStart,
		agentpkg.EventVerificationPassed,
	}
	result := agentpkg.RunResult{Status: agentpkg.StatusSuccess}
	for _, eventType := range eventTypes {
		event := agentpkg.SessionEvent{Type: eventType, Timestamp: time.Now().UTC()}
		result.LifecycleEvents = append(result.LifecycleEvents, event)
		if err := request.OnLifecycleEvent(event); err != nil {
			return result, err
		}
		r.published <- eventType
		<-r.release
	}
	return result, nil
}

func TestAgentSessionUpdatesFirstPassStatesWhileRunIsActive(t *testing.T) {
	runner := &liveFirstPassStateRunner{
		sessionID: make(chan string, 1),
		published: make(chan agentpkg.SessionEventType),
		release:   make(chan struct{}),
	}
	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)
	req := httptest.NewRequest(http.MethodPost, "/api/agent/session", bytes.NewReader([]byte(`{
        "workspace":"C:/repo",
        "task":"edit and verify source"
    }`)))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(recorder, req)
	}()

	sessionID := <-runner.sessionID
	wantStates := []agentpkg.SessionState{
		agentpkg.SessionStateEditing,
		agentpkg.SessionStateDiffDetected,
		agentpkg.SessionStateVerifying,
		agentpkg.SessionStateVerified,
	}
	for i, wantState := range wantStates {
		select {
		case <-runner.published:
		case <-time.After(2 * time.Second):
			t.Fatalf("runner did not publish lifecycle event %d", i)
		}
		snapshot, err := store.Load(sessionID)
		if err != nil {
			t.Fatalf("Load active session after event %d: %v", i, err)
		}
		if snapshot.State != wantState {
			t.Fatalf("active state after event %d = %q, want %q", i, snapshot.State, wantState)
		}
		runner.release <- struct{}{}
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("session request did not finish")
	}

	waitForPersistedSessionState(
		t,
		store,
		sessionID,
		agentpkg.SessionStateVerified,
	)
	waitForPersistedSessionEvent(t, store, sessionID, agentpkg.EventCompleted)

	events, err := store.Events(sessionID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	wantOrder := []agentpkg.SessionEventType{
		agentpkg.EventSessionCreated,
		agentpkg.EventEditingStarted,
		agentpkg.EventDiffDetected,
		agentpkg.EventVerificationStart,
		agentpkg.EventVerificationPassed,
		agentpkg.EventCompleted,
	}
	assertLifecycleEventRelativeOrder(t, events, wantOrder)
}

func TestAgentSessionExposesRealFileDiffBeforeAgentFinishes(t *testing.T) {
	root := t.TempDir()
	runner := &liveFileDiffRunner{
		published: make(chan struct{}),
		release:   make(chan struct{}),
	}
	defer func() {
		select {
		case <-runner.release:
		default:
			close(runner.release)
		}
	}()
	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)
	body, err := json.Marshal(map[string]string{
		"workspace": root,
		"task":      "write a source file",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/agent/session", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	started := httptest.NewRecorder()
	router.ServeHTTP(started, request)
	if started.Code != http.StatusOK {
		t.Fatalf("session start status %d: %s", started.Code, started.Body.String())
	}
	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(started.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.ID == "" {
		t.Fatal("agent session did not return its ID")
	}
	select {
	case <-runner.published:
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not publish its file diff")
	}

	eventsRecorder := httptest.NewRecorder()
	router.ServeHTTP(eventsRecorder, httptest.NewRequest(
		http.MethodGet,
		"/api/agent/session/"+accepted.ID+"/events",
		nil,
	))
	if eventsRecorder.Code != http.StatusOK {
		t.Fatalf("events status %d: %s", eventsRecorder.Code, eventsRecorder.Body.String())
	}
	var events []agentpkg.SessionEvent
	if err := json.Unmarshal(eventsRecorder.Body.Bytes(), &events); err != nil {
		t.Fatal(err)
	}
	var change *agentpkg.SessionEvent
	for i := range events {
		if events[i].Type == agentpkg.EventFilesystemChange {
			change = &events[i]
			break
		}
	}
	if change == nil || change.FilesystemDiff == nil || len(change.FilesystemDiff.Files) != 1 {
		t.Fatalf("live file-change event is missing its real diff: %+v", events)
	}
	file := change.FilesystemDiff.Files[0]
	if file.Path != "live-change.go" || !strings.Contains(file.Unified, "+package main") {
		t.Fatalf("live diff filename or code is wrong: %+v", file)
	}
	snapshot, err := store.Load(accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State == agentpkg.SessionStateVerified || snapshot.State == agentpkg.SessionStateFailed {
		t.Fatalf("file diff arrived only after the task had finished: %q", snapshot.State)
	}
	close(runner.release)
	waitForPersistedSessionEvent(t, store, accepted.ID, agentpkg.EventCompleted)
}

func assertLifecycleEventRelativeOrder(
	t *testing.T,
	events []agentpkg.SessionEvent,
	want []agentpkg.SessionEventType,
) {
	t.Helper()
	next := 0
	for _, event := range events {
		if next < len(want) && event.Type == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("events do not contain relative order %v: %#v", want, events)
	}
}

func TestLiveAgentLifecycleStatePreservesRepairStatesForInitialEditEvents(t *testing.T) {
	states := []agentpkg.SessionState{
		agentpkg.SessionStateVerifyFailed,
		agentpkg.SessionStateRepairing,
		agentpkg.SessionStateReverifying,
	}
	events := []agentpkg.SessionEventType{
		agentpkg.EventEditingStarted,
		agentpkg.EventDiffDetected,
	}

	for _, state := range states {
		for _, eventType := range events {
			if got := liveAgentLifecycleState(state, eventType); got != state {
				t.Fatalf(
					"state %q with event %q became %q",
					state,
					eventType,
					got,
				)
			}
		}
	}
}

func (r *liveSessionStateRunner) Run(
	_ context.Context,
	request agentpkg.RunRequest,
) (agentpkg.RunResult, error) {
	r.sessionID <- request.SessionID

	if request.OnLifecycleEvent == nil {
		return agentpkg.RunResult{}, context.Canceled
	}

	eventTypes := []agentpkg.SessionEventType{
		agentpkg.EventVerificationStart,
		agentpkg.EventVerificationFailed,
		agentpkg.EventRepairStarted,
		agentpkg.EventRepairCompleted,
		agentpkg.EventVerificationStart,
		agentpkg.EventVerificationPassed,
	}

	result := agentpkg.RunResult{
		Status:       agentpkg.StatusSuccess,
		FinalSummary: "verified",
	}

	for _, eventType := range eventTypes {
		event := agentpkg.SessionEvent{
			Type:      eventType,
			Timestamp: time.Now().UTC(),
			Message:   string(eventType),
		}
		result.LifecycleEvents = append(result.LifecycleEvents, event)

		if err := request.OnLifecycleEvent(event); err != nil {
			return result, err
		}

		r.published <- eventType

		// Keep Run blocked after every callback so persistent state can
		// be inspected before the next lifecycle event is emitted.
		<-r.release
	}

	return result, nil
}

func TestAgentSessionUpdatesStateWhileRunIsActive(t *testing.T) {
	runner := &liveSessionStateRunner{
		sessionID: make(chan string, 1),
		published: make(chan agentpkg.SessionEventType),
		release:   make(chan struct{}),
	}

	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	body := []byte(`{
        "workspace":"C:/repo",
        "task":"verify source"
    }`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		defer close(done)
		router.ServeHTTP(recorder, req)
	}()

	var sessionID string

	select {
	case sessionID = <-runner.sessionID:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not receive session ID")
	}

	wantStates := []agentpkg.SessionState{
		agentpkg.SessionStateVerifying,
		agentpkg.SessionStateVerifyFailed,
		agentpkg.SessionStateRepairing,
		agentpkg.SessionStateReverifying,
		agentpkg.SessionStateReverifying,
		agentpkg.SessionStateVerified,
	}

	for i, wantState := range wantStates {
		select {
		case <-runner.published:
		case <-time.After(2 * time.Second):
			t.Fatalf("runner did not publish lifecycle event %d", i)
		}

		snapshot, err := store.Load(sessionID)
		if err != nil {
			t.Fatalf("Load active session after event %d: %v", i, err)
		}

		if snapshot.State != wantState {
			t.Fatalf(
				"active state after event %d = %q, want %q",
				i,
				snapshot.State,
				wantState,
			)
		}

		runner.release <- struct{}{}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("session request did not finish")
	}

	waitForPersistedSessionState(
		t,
		store,
		sessionID,
		agentpkg.SessionStateVerified,
	)

	events, err := store.Events(sessionID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	wantEventCount := make(map[agentpkg.SessionEventType]int)
	for _, eventType := range []agentpkg.SessionEventType{
		agentpkg.EventVerificationStart,
		agentpkg.EventVerificationFailed,
		agentpkg.EventRepairStarted,
		agentpkg.EventRepairCompleted,
		agentpkg.EventVerificationPassed,
	} {
		wantEventCount[eventType]++
	}
	// Verification starts once before repair and once for reverification.
	wantEventCount[agentpkg.EventVerificationStart]++

	gotEventCount := make(map[agentpkg.SessionEventType]int)
	for _, event := range events {
		gotEventCount[event.Type]++
	}

	for eventType, want := range wantEventCount {
		if got := gotEventCount[eventType]; got != want {
			t.Fatalf(
				"event %q count = %d, want %d; events=%#v",
				eventType,
				got,
				want,
				events,
			)
		}
	}
}

type liveLifecycleDuplicateRunner struct {
	sessionID string
}

func (r *liveLifecycleDuplicateRunner) Run(
	_ context.Context,
	request agentpkg.RunRequest,
) (agentpkg.RunResult, error) {
	r.sessionID = request.SessionID
	events := []agentpkg.SessionEvent{
		{Type: agentpkg.EventEditingStarted, Timestamp: time.Now().UTC()},
		{Type: agentpkg.EventDiffDetected, Timestamp: time.Now().UTC()},
		{Type: agentpkg.EventVerificationStart, Timestamp: time.Now().UTC()},
	}

	for _, event := range events {
		if err := request.OnLifecycleEvent(event); err != nil {
			return agentpkg.RunResult{}, err
		}
	}

	return agentpkg.RunResult{
		Status:          agentpkg.StatusSuccess,
		LifecycleEvents: events,
		FinalSummary:    "verified",
	}, nil
}

func TestAgentSessionDoesNotDuplicateLiveLifecycleEventsAfterCompletion(t *testing.T) {
	runner := &liveLifecycleDuplicateRunner{}
	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session",
		bytes.NewReader([]byte(`{
            "workspace":"C:/repo",
            "task":"verify source"
        }`)),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted session: %v", err)
	}
	waitForPersistedSessionState(
		t,
		store,
		accepted.ID,
		agentpkg.SessionStateVerified,
	)

	events, err := store.Events(accepted.ID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	for _, eventType := range []agentpkg.SessionEventType{
		agentpkg.EventEditingStarted,
		agentpkg.EventDiffDetected,
		agentpkg.EventVerificationStart,
	} {
		count := 0
		for _, event := range events {
			if event.Type == eventType {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("event %q count = %d, want 1; events=%#v", eventType, count, events)
		}
	}
}
