package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentpkg "github.com/ollama/ollama/internal/agent"
)

type liveSessionStateRunner struct {
	sessionID chan string
	published chan agentpkg.SessionEventType
	release   chan struct{}
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
	event := agentpkg.SessionEvent{
		Type:      agentpkg.EventVerificationStart,
		Timestamp: time.Now().UTC(),
		Message:   "verification started",
	}

	if err := request.OnLifecycleEvent(event); err != nil {
		return agentpkg.RunResult{}, err
	}

	return agentpkg.RunResult{
		Status:          agentpkg.StatusSuccess,
		LifecycleEvents: []agentpkg.SessionEvent{event},
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

	events, err := store.Events(runner.sessionID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	count := 0
	for _, event := range events {
		if event.Type == agentpkg.EventVerificationStart {
			count++
		}
	}

	if count != 1 {
		t.Fatalf("verification_started count = %d, want 1; events=%#v", count, events)
	}
}
