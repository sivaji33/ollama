package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	agentpkg "github.com/ollama/ollama/internal/agent"
)

func TestAgentSessionPersistsEngineLifecycleEventsInOrder(t *testing.T) {
	now := time.Now().UTC()

	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			Status: agentpkg.StatusSuccess,
			LifecycleEvents: []agentpkg.SessionEvent{
				{
					Type:      agentpkg.EventVerificationStart,
					Timestamp: now.Add(1 * time.Second),
					Message:   "verification started",
				},
				{
					Type:      agentpkg.EventVerificationFailed,
					Timestamp: now.Add(2 * time.Second),
					Message:   "verification failed",
				},
				{
					Type:      agentpkg.EventRepairStarted,
					Timestamp: now.Add(3 * time.Second),
					Message:   "repair started",
				},
				{
					Type:      agentpkg.EventRepairCompleted,
					Timestamp: now.Add(4 * time.Second),
					Message:   "repair completed",
				},
				{
					Type:      agentpkg.EventVerificationStart,
					Timestamp: now.Add(5 * time.Second),
					Message:   "verification started",
				},
				{
					Type:      agentpkg.EventVerificationPassed,
					Timestamp: now.Add(6 * time.Second),
					Message:   "verification passed",
				},
			},
			FinalSummary: "verified after repair",
		},
	}

	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	body := []byte(`{
        "workspace":"` + filepath.ToSlash(t.TempDir()) + `",
        "task":"repair and verify source"
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
			"status = %d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}

	events, err := store.Events(runner.request.SessionID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	want := []agentpkg.SessionEventType{
		agentpkg.EventSessionCreated,
		agentpkg.EventVerificationStart,
		agentpkg.EventVerificationFailed,
		agentpkg.EventRepairStarted,
		agentpkg.EventRepairCompleted,
		agentpkg.EventVerificationStart,
		agentpkg.EventVerificationPassed,
		agentpkg.EventCompleted,
	}

	if len(events) != len(want) {
		t.Fatalf(
			"event count = %d, want %d; events=%#v",
			len(events),
			len(want),
			events,
		)
	}

	for i, wantType := range want {
		if events[i].Type != wantType {
			t.Fatalf(
				"event %d = %q, want %q; events=%#v",
				i,
				events[i].Type,
				wantType,
				events,
			)
		}
	}

	if events[3].Message != "repair started" {
		t.Fatalf(
			"repair event message = %q",
			events[3].Message,
		)
	}

	if events[6].Message != "verification passed" {
		t.Fatalf(
			"verification pass message = %q",
			events[6].Message,
		)
	}
}
