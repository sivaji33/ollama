package server

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"net/http"
	"net/http/httptest"

	agentpkg "github.com/ollama/ollama/internal/agent"
)

func initSessionContextRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(root, "main.go"),
		[]byte("package main\n"),
		0o600,
	); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte("module example.com/sessioncontext\n\ngo 1.25\n"),
		0o600,
	); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	for _, argv := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", argv...)
		cmd.Dir = root

		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf(
				"git %v: %v\n%s",
				argv,
				err,
				output,
			)
		}
	}

	return root
}

func TestAgentSessionPersistsContextBuiltBeforeCompletion(t *testing.T) {
	workspace := initSessionContextRepo(t)

	runner := &sessionAPITestRunner{
		result: agentpkg.RunResult{
			Status:       agentpkg.StatusSuccess,
			ContextBuilt: true,
			FinalSummary: "verified",
		},
	}

	server, store := newSessionAPITestServer(t, runner)
	router := sessionAPIRouter(server)

	body := []byte(`{
        "workspace":"` + filepath.ToSlash(workspace) + `",
        "task":"modify main package"
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

	if len(events) < 3 {
		t.Fatalf(
			"events = %#v, want session_created, context_built, completed",
			events,
		)
	}

	if events[0].Type != agentpkg.EventSessionCreated {
		t.Fatalf(
			"events[0] = %q, want %q",
			events[0].Type,
			agentpkg.EventSessionCreated,
		)
	}

	if events[1].Type != agentpkg.EventContextBuilt {
		t.Fatalf(
			"events[1] = %q, want %q",
			events[1].Type,
			agentpkg.EventContextBuilt,
		)
	}

	if events[len(events)-1].Type != agentpkg.EventCompleted {
		t.Fatalf(
			"last event = %q, want %q",
			events[len(events)-1].Type,
			agentpkg.EventCompleted,
		)
	}

	if events[1].Message == "" {
		t.Fatalf("context_built event message was empty")
	}
}
