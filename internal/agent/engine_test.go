package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ollama/ollama/api"
)

type scriptedChat struct {
	responses []api.ChatResponse
	requests  []api.ChatRequest
}

func patchCall() api.ChatResponse {
	return api.ChatResponse{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{
		{ID: "patch-1", Function: api.ToolCallFunction{
			Name:      "apply_patch",
			Arguments: args(map[string]any{"path": "main.go", "old_text": "package old", "new_text": "package main"}),
		}},
	}}}
}

func (s *scriptedChat) Chat(_ context.Context, req *api.ChatRequest, fn api.ChatResponseFunc) error {
	s.requests = append(s.requests, *req)
	r := s.responses[0]
	s.responses = s.responses[1:]
	return fn(r)
}

func args(values map[string]any) api.ToolCallFunctionArguments {
	a := api.NewToolCallFunctionArguments()
	for k, v := range values {
		a.Set(k, v)
	}
	return a
}

func initRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"init"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}, {"add", "."}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", argv...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", argv, err, out)
		}
	}
	return root
}

func TestEngineCompletesOnlyAfterRealEditAndVerification(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{
			{ID: "1", Function: api.ToolCallFunction{
				Name:      "apply_patch",
				Arguments: args(map[string]any{"path": "main.go", "old_text": "package old", "new_text": "package main"}),
			}},
		}}},
		{Message: api.Message{Role: "assistant", Content: "done"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || len(result.ChangedFiles) != 1 || len(result.VerificationResults) != 1 || !result.VerificationResults[0].Passed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestEngineRejectsSuccessWithoutSourceDiff(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", Content: "done"}},
		{Message: api.Message{Role: "assistant", Content: "still done"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == StatusSuccess {
		t.Fatalf("model prose bypassed diff gate: %+v", result)
	}
	if result.StepsExecuted != 2 {
		t.Fatalf("steps executed = %d, want 2", result.StepsExecuted)
	}
}

func TestEngineRetriesTextOnlyResponseBeforeAnyEdit(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", Content: "```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"main.go\"}}\n```"}},
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{
			{ID: "read-1", Function: api.ToolCallFunction{Name: "read_file", Arguments: args(map[string]any{"path": "main.go"})}},
		}}},
		patchCall(),
		{Message: api.Message{Role: "assistant", Content: "completed"}},
	}}

	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.StepsExecuted != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(chat.requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(chat.requests))
	}
	secondMessages := chat.requests[1].Messages
	if !strings.Contains(secondMessages[len(secondMessages)-1].Content, "did not execute a tool") {
		t.Fatalf("missing corrective message: %+v", secondMessages)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(result.ToolCalls))
	}
	wantKinds := []StepKind{StepToolFreeText, StepToolCall, StepToolCall, StepEditDetected, StepToolFreeText, StepVerification}
	if len(result.StepEvents) != len(wantKinds) {
		t.Fatalf("step events = %+v, want kinds %v", result.StepEvents, wantKinds)
	}
	for i, want := range wantKinds {
		if result.StepEvents[i].Kind != want {
			t.Fatalf("event %d kind = %q, want %q", i, result.StepEvents[i].Kind, want)
		}
	}
}

func TestEngineRetriesEmptyResponseBeforeAnyEdit(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant"}},
		patchCall(),
		{Message: api.Message{Role: "assistant", Content: "completed"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.StepsExecuted != 3 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.StepEvents) == 0 || result.StepEvents[0].Kind != StepToolFreeEmpty {
		t.Fatalf("missing empty-response diagnostic: %+v", result.StepEvents)
	}
}

func TestEngineExhaustsMaxStepsOnRepeatedToolFreeResponses(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", Content: "first"}},
		{Message: api.Message{Role: "assistant"}},
		{Message: api.Message{Role: "assistant", Content: "third"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", MaxSteps: 3, Verify: []string{"this must not run"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusFailed || result.StepsExecuted != 3 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.VerificationResults) != 0 {
		t.Fatalf("verification ran without a diff: %+v", result.VerificationResults)
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("assistant prose was treated as tool calls: %+v", result.ToolCalls)
	}
	if got := []StepKind{result.StepEvents[0].Kind, result.StepEvents[1].Kind, result.StepEvents[2].Kind}; got[0] != StepToolFreeText || got[1] != StepToolFreeEmpty || got[2] != StepToolFreeText {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
}

func TestAgentFirstRequestRequiresNativeToolUse(t *testing.T) {
	root := initRepo(t)
	pseudoCall := `{"name":"apply_patch","arguments":{"path":"main.go"}}`
	chat := &scriptedChat{responses: []api.ChatResponse{{Message: api.Message{Role: "assistant", Content: pseudoCall}}}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(chat.requests))
	}
	req := chat.requests[0]
	if req.Stream == nil || *req.Stream {
		t.Fatal("agent model request must disable streaming")
	}
	wantNames := []string{"search_files", "read_file", "apply_patch", "shell", "git_diff"}
	if len(req.Tools) != len(wantNames) {
		t.Fatalf("tools = %d, want %d", len(req.Tools), len(wantNames))
	}
	for i, want := range wantNames {
		if req.Tools[i].Function.Name != want {
			t.Fatalf("tool %d = %q, want %q", i, req.Tools[i].Function.Name, want)
		}
	}
	system := req.Messages[0].Content
	for _, phrase := range []string{"modification task", "native", "assistant prose", "json printed as assistant content", "meaningful source edit", "git_diff", "verification"} {
		if !strings.Contains(strings.ToLower(system), phrase) {
			t.Errorf("system prompt missing %q: %s", phrase, system)
		}
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("pseudo-tool JSON was executed: %+v", result.ToolCalls)
	}
}

func TestEngineRecordsVerificationAndRepairDiagnostics(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		patchCall(),
		{Message: api.Message{Role: "assistant", Content: "verify"}},
		{Message: api.Message{Role: "assistant", Content: "verify again"}},
		{Message: api.Message{Role: "assistant", Content: "final verify"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", Verify: []string{"git diff --exit-code"}})
	if err != nil {
		t.Fatal(err)
	}
	var verification, repairs int
	for _, event := range result.StepEvents {
		switch event.Kind {
		case StepVerification:
			verification++
		case StepRepair:
			repairs++
		}
	}
	if verification != 3 || repairs != MaxRepairAttempts {
		t.Fatalf("verification events = %d, repair events = %d; events: %+v", verification, repairs, result.StepEvents)
	}
	if result.Status != StatusFailed {
		t.Fatalf("failed verification became success: %+v", result)
	}
}
