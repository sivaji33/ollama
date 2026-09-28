package agent

import (
	"context"
	"errors"
	"fmt"
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
	if len(s.responses) == 0 {
		return errors.New("scripted chat exhausted")
	}
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
		{Message: api.Message{Role: "assistant", Content: "done again"}},
		{Message: api.Message{Role: "assistant", Content: "done again"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == StatusSuccess {
		t.Fatalf("model prose bypassed diff gate: %+v", result)
	}
	if result.StepsExecuted != 4 {
		t.Fatalf("steps executed = %d, want 4 consecutive unproductive turns", result.StepsExecuted)
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
	if result.Status != StatusSuccess || result.StepsExecuted != 3 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(chat.requests) != 3 {
		t.Fatalf("model requests = %d, want 3", len(chat.requests))
	}
	secondMessages := chat.requests[1].Messages
	if !strings.Contains(secondMessages[len(secondMessages)-1].Content, "did not execute a tool") {
		t.Fatalf("missing corrective message: %+v", secondMessages)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(result.ToolCalls))
	}
	wantKinds := []StepKind{StepToolFreeText, StepToolCall, StepToolCall, StepEditDetected, StepVerification}
	if len(result.StepEvents) != len(wantKinds) {
		t.Fatalf("step events = %+v, want kinds %v", result.StepEvents, wantKinds)
	}
	for i, want := range wantKinds {
		if result.StepEvents[i].Kind != want {
			t.Fatalf("event %d kind = %q, want %q", i, result.StepEvents[i].Kind, want)
		}
	}
}

func TestEngineBoundsFollowupConversationAndDoesNotRepeatRepositoryInventory(t *testing.T) {
	root := initRepo(t)
	readCall := func(id string) api.ChatResponse {
		return api.ChatResponse{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
			ID: id, Function: api.ToolCallFunction{Name: "read_file", Arguments: args(map[string]any{"path": "main.go"})},
		}}}}
	}
	chat := &scriptedChat{responses: []api.ChatResponse{
		readCall("read-1"), readCall("read-2"), readCall("read-3"), readCall("read-4"),
		{Message: api.Message{Role: "assistant", Content: "no edit"}},
	}}
	_, err := NewEngine(chat).Run(context.Background(), RunRequest{
		Model: "test", Workspace: root, Task: "inspect then change package",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.requests) != 5 {
		t.Fatalf("requests = %d, want 5", len(chat.requests))
	}
	if !strings.Contains(chat.requests[0].Messages[1].Content, "Tracked files:") {
		t.Fatal("first turn lost repository inventory")
	}
	for i, request := range chat.requests[1:] {
		if strings.Contains(request.Messages[1].Content, "Tracked files:") {
			t.Fatalf("followup %d repeated repository inventory", i+2)
		}
		if len(request.Messages) > maxAgentConversationMessages {
			t.Fatalf("followup %d messages = %d, want <= %d", i+2, len(request.Messages), maxAgentConversationMessages)
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
	if result.Status != StatusSuccess || result.StepsExecuted != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.StepEvents) == 0 || result.StepEvents[0].Kind != StepToolFreeEmpty {
		t.Fatalf("missing empty-response diagnostic: %+v", result.StepEvents)
	}
}

func TestEngineStopsAfterFourUnproductiveToolFreeTurns(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", Content: "first"}},
		{Message: api.Message{Role: "assistant"}},
		{Message: api.Message{Role: "assistant", Content: "third"}},
		{Message: api.Message{Role: "assistant", Content: "fourth"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", Verify: []string{"this must not run"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusFailed || result.StepsExecuted != 4 || !strings.Contains(result.FinalSummary, "stuck") {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.VerificationResults) != 0 || len(result.ToolCalls) != 0 {
		t.Fatalf("prose should not trigger tools or verification: %+v", result)
	}
	if got := []StepKind{result.StepEvents[0].Kind, result.StepEvents[1].Kind, result.StepEvents[2].Kind}; got[0] != StepToolFreeText || got[1] != StepToolFreeEmpty || got[2] != StepToolFreeText {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
}

func TestAgentFirstRequestRequiresNativeToolUse(t *testing.T) {
	root := initRepo(t)
	pseudoCall := `{"name":"apply_patch","arguments":{"path":"main.go"}}`
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", Content: pseudoCall}},
		{Message: api.Message{Role: "assistant", Content: pseudoCall}},
		{Message: api.Message{Role: "assistant", Content: pseudoCall}},
		{Message: api.Message{Role: "assistant", Content: pseudoCall}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package"})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(chat.requests))
	}
	req := chat.requests[0]
	if req.Stream == nil || *req.Stream {
		t.Fatal("agent model request must disable streaming")
	}
	if got := req.Options["num_ctx"]; got != agentContextWindow {
		t.Fatalf("agent num_ctx = %v, want %d", got, agentContextWindow)
	}
	if req.Truncate == nil || !*req.Truncate {
		t.Fatal("agent request must enable bounded server-side truncation")
	}
	wantNames := []string{"search_files", "list_files", "read_file", "write_file", "apply_patch", "multi_edit", "delete_file", "move_file", "shell", "git_diff"}
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
		{Message: api.Message{Role: "assistant", Content: "still no edit"}},
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
	if verification != 1 || repairs != 1 {
		t.Fatalf("unchanged failing source was verified repeatedly: verification=%d repairs=%d events=%+v", verification, repairs, result.StepEvents)
	}
	if result.Status != StatusFailed {
		t.Fatalf("failed verification became success: %+v", result)
	}
}

func TestEngineCanUseMoreThanTwelveProductiveSteps(t *testing.T) {
	root := initRepo(t)
	var responses []api.ChatResponse
	for i := 0; i < 13; i++ {
		name := fmt.Sprintf("file%02d.go", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, api.ChatResponse{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
			ID: fmt.Sprintf("read-%d", i), Function: api.ToolCallFunction{Name: "read_file", Arguments: args(map[string]any{"path": name})},
		}}}})
	}
	responses = append(responses, patchCall())
	chat := &scriptedChat{responses: responses}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "inspect all files and fix main.go", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.StepsExecuted != 14 {
		t.Fatalf("agent did not finish after fourteen productive steps: %+v", result)
	}
}

func TestEngineAutomaticallyVerifiesAfterPatchWithoutAnotherModelTurn(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{patchCall()}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.StepsExecuted != 1 || len(result.VerificationResults) != 1 || !result.VerificationResults[0].Passed {
		t.Fatalf("patch was not automatically verified: %+v", result)
	}
}

func TestEngineRepairsMoreThanTwoTimesWhenSourceKeepsProgressing(t *testing.T) {
	root := initRepo(t)
	packages := []string{"old", "first", "second", "third", "final"}
	var responses []api.ChatResponse
	for i := 0; i < len(packages)-1; i++ {
		responses = append(responses, api.ChatResponse{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
			ID: fmt.Sprintf("patch-%d", i), Function: api.ToolCallFunction{Name: "apply_patch", Arguments: args(map[string]any{
				"path": "main.go", "old_text": "package " + packages[i], "new_text": "package " + packages[i+1],
			})},
		}}}})
	}
	chat := &scriptedChat{responses: responses}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "reach final version", Verify: []string{"git grep -q \"package final\" -- main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.StepsExecuted != 4 || len(result.VerificationResults) != 1 || !result.VerificationResults[0].Passed {
		t.Fatalf("productive repairs stopped prematurely: %+v", result)
	}
}

func TestEngineStopsRepeatedReadLoop(t *testing.T) {
	root := initRepo(t)
	var responses []api.ChatResponse
	for i := 0; i < 5; i++ {
		responses = append(responses, api.ChatResponse{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
			ID: fmt.Sprintf("repeat-%d", i), Function: api.ToolCallFunction{Name: "read_file", Arguments: args(map[string]any{"path": "main.go"})},
		}}}})
	}
	result, err := NewEngine(&scriptedChat{responses: responses}).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusFailed || result.StepsExecuted != 5 || !strings.Contains(result.FinalSummary, "stuck") {
		t.Fatalf("repeated identical reads did not stop: %+v", result)
	}
}

type cancelAfterModelResponse struct {
	cancel context.CancelFunc
	calls  int
}

func (c *cancelAfterModelResponse) Chat(_ context.Context, _ *api.ChatRequest, fn api.ChatResponseFunc) error {
	c.calls++
	if err := fn(patchCall()); err != nil {
		return err
	}
	c.cancel()
	return nil
}

func TestEngineCancellationPreventsToolsAndFurtherModelTurns(t *testing.T) {
	root := initRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chat := &cancelAfterModelResponse{cancel: cancel}
	result, err := NewEngine(chat).Run(ctx, RunRequest{Model: "test", Workspace: root, Task: "change package"})
	if !errors.Is(err, context.Canceled) || result.Status != StatusFailed || chat.calls != 1 || len(result.ToolCalls) != 0 {
		t.Fatalf("cancel did not stop agent before patch: result=%+v err=%v calls=%d", result, err, chat.calls)
	}
	data, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || string(data) != "package old\n" {
		t.Fatalf("cancellation unexpectedly modified source: %q %v", data, err)
	}
}
