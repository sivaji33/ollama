package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/ollama/ollama/internal/agent"
)

func TestAgentSessionEditorContextIsExecutionOnly(t *testing.T) {
	workspace := initSessionContextRepo(t)
	runner := &sessionAPITestRunner{result: agentpkg.RunResult{Status: agentpkg.StatusSuccess}}
	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)
	body, err := json.Marshal(map[string]any{
		"workspace": workspace,
		"task":      "Fix the selected code.",
		"editor_context": map[string]any{
			"selection": map[string]any{"text": "const current = true"},
			"metadata":  map[string]any{"truncated": false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/agent/session", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var accepted agentpkg.SessionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	snapshot := waitForPersistedSessionState(t, store, accepted.ID, agentpkg.SessionStateVerified)
	runTask := runner.lastRequest().Task
	if !strings.Contains(runTask, "[CURRENT EDITOR CONTEXT]") || !strings.Contains(runTask, "const current = true") {
		t.Fatalf("runner task did not receive editor context: %q", runTask)
	}
	if snapshot.Task != "Fix the selected code." {
		t.Fatalf("persisted task = %q, want original task only", snapshot.Task)
	}
}
