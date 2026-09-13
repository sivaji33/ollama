package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
	agentpkg "github.com/ollama/ollama/internal/agent"
)

type lifecycleEngineChat struct {
	responses []api.ChatResponse
}

func (c *lifecycleEngineChat) Chat(
	_ context.Context,
	_ *api.ChatRequest,
	fn api.ChatResponseFunc,
) error {
	response := c.responses[0]
	c.responses = c.responses[1:]
	return fn(response)
}

func lifecycleToolArgs(values map[string]any) api.ToolCallFunctionArguments {
	arguments := api.NewToolCallFunctionArguments()
	for key, value := range values {
		arguments.Set(key, value)
	}
	return arguments
}

func TestAgentSessionPersistsRealEngineEditLifecycleInRelativeOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package old\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	for _, argv := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		command := exec.Command("git", argv...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", argv, err, output)
		}
	}

	chat := &lifecycleEngineChat{
		responses: []api.ChatResponse{
			{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
				ID: "edit-1",
				Function: api.ToolCallFunction{
					Name: "apply_patch",
					Arguments: lifecycleToolArgs(map[string]any{
						"path":     "main.go",
						"old_text": "package old",
						"new_text": "package main",
					}),
				},
			}}}},
			{Message: api.Message{Role: "assistant", Content: "source verified"}},
		},
	}
	server, store := newSessionAPITestServer(t, agentpkg.NewEngine(chat))
	router := sessionAPIRouter(server)
	body := []byte(`{
        "workspace":"` + filepath.ToSlash(root) + `",
        "task":"change package",
        "verify":["git diff --check"]
    }`)
	req := httptest.NewRequest(http.MethodPost, "/api/agent/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	entries, err := os.ReadDir(store.Root())
	if err != nil || len(entries) != 1 {
		t.Fatalf("session directories = %v, err=%v", entries, err)
	}
	events, err := store.Events(entries[0].Name())
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	assertLifecycleEventRelativeOrder(t, events, []agentpkg.SessionEventType{
		agentpkg.EventSessionCreated,
		agentpkg.EventEditingStarted,
		agentpkg.EventDiffDetected,
		agentpkg.EventVerificationStart,
		agentpkg.EventVerificationPassed,
		agentpkg.EventCompleted,
	})
}

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
