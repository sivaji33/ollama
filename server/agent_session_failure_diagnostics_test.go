package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/ollama/ollama/internal/agent"
)

const engineEarlyFailure = `build repository context: git rev-parse --abbrev-ref HEAD failed: exec: "git": executable file not found in %PATH%`

func createSessionForDiagnostics(
	t *testing.T,
	runner *sessionAPITestRunner,
) (*agentpkg.SessionStore, agentpkg.SessionSnapshot) {
	t.Helper()

	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/session",
		bytes.NewReader([]byte(`{
            "workspace":"C:/repo",
            "task":"change main.py"
        }`)),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted session: %v", err)
	}

	return store, accepted
}

func failedSessionEvent(t *testing.T, store *agentpkg.SessionStore, sessionID string) agentpkg.SessionEvent {
	t.Helper()

	events, err := store.Events(sessionID)
	if err != nil {
		t.Fatalf("Events(%q): %v", sessionID, err)
	}

	for i := range events {
		if events[i].Type == agentpkg.EventFailed {
			return events[i]
		}
	}

	t.Fatalf("no failed event recorded for %q: %+v", sessionID, events)
	return agentpkg.SessionEvent{}
}

// An early engine failure returns before a summary exists. The server must
// persist the runner error so both the session record and the event log
// explain why the session failed instead of reporting an empty failure.
func TestAgentSessionPersistsRunnerErrorWhenEngineFailsWithoutSummary(t *testing.T) {
	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			SessionID: "session-http-diagnostics",
			Status:    agentpkg.StatusFailed,
		},
		err: errors.New(engineEarlyFailure),
	}

	store, accepted := createSessionForDiagnostics(t, runner)

	snapshot := waitForPersistedSessionState(
		t,
		store,
		accepted.ID,
		agentpkg.SessionStateFailed,
	)

	if !strings.Contains(snapshot.FinalSummary, engineEarlyFailure) {
		t.Fatalf(
			"final summary = %q, want it to carry the runner error %q",
			snapshot.FinalSummary,
			engineEarlyFailure,
		)
	}

	event := failedSessionEvent(t, store, accepted.ID)
	if !strings.Contains(event.Message, engineEarlyFailure) {
		t.Fatalf(
			"failed event message = %q, want it to carry the runner error %q",
			event.Message,
			engineEarlyFailure,
		)
	}
}

// A failure that already explains itself keeps its own summary instead of
// being overwritten by the generic runner error.
func TestAgentSessionKeepsRunnerSummaryOnFailure(t *testing.T) {
	const stuckSummary = "agent stuck: 4 consecutive turns without new observations or meaningful source changes"

	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			SessionID:    "session-http-diagnostics-summary",
			Status:       agentpkg.StatusFailed,
			FinalSummary: stuckSummary,
		},
		err: errors.New("runner reached stagnation"),
	}

	store, accepted := createSessionForDiagnostics(t, runner)

	snapshot := waitForPersistedSessionState(
		t,
		store,
		accepted.ID,
		agentpkg.SessionStateFailed,
	)

	if snapshot.FinalSummary != stuckSummary {
		t.Fatalf(
			"final summary = %q, want the runner summary %q",
			snapshot.FinalSummary,
			stuckSummary,
		)
	}

	event := failedSessionEvent(t, store, accepted.ID)
	if !strings.Contains(event.Message, stuckSummary) {
		t.Fatalf(
			"failed event message = %q, want it to carry %q",
			event.Message,
			stuckSummary,
		)
	}
}
