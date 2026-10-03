package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ollama/ollama/api"
)

func TestContextForUsableRAMSelectsConfiguredTiers(t *testing.T) {
	tests := []struct {
		usableGB float64
		want     int
	}{
		{usableGB: 3.99, want: 4096},
		{usableGB: 4, want: 8192},
		{usableGB: 7.99, want: 8192},
		{usableGB: 8, want: 16384},
		{usableGB: 15.99, want: 16384},
		{usableGB: 16, want: 32768},
		{usableGB: 32, want: 32768},
		{usableGB: 64, want: 32768},
	}
	for _, test := range tests {
		if got := contextForUsableRAM(test.usableGB); got != test.want {
			t.Errorf("contextForUsableRAM(%.2f) = %d, want %d", test.usableGB, got, test.want)
		}
	}
}

func TestContextStatusTiersUseAvailableRAMAfterReserve(t *testing.T) {
	t.Setenv(agentMaxContextEnv, "32768")
	t.Setenv(agentMinContextEnv, "4096")
	t.Setenv(agentRAMReserveEnv, "4")
	tests := []struct {
		availableGB uint64
		want        int
	}{
		{availableGB: 4, want: 4096},
		{availableGB: 8, want: 8192},
		{availableGB: 16, want: 16384},
		{availableGB: 32, want: 32768},
		{availableGB: 40, want: 32768},
	}
	for _, test := range tests {
		status := contextStatus(64<<30, test.availableGB<<30)
		if status.SelectedContextTokens != test.want {
			t.Errorf("available RAM %d GB selected %d tokens, want %d", test.availableGB, status.SelectedContextTokens, test.want)
		}
	}
}

func TestContextStatusReservesRAMAndAppliesMaximum(t *testing.T) {
	t.Setenv(agentMaxContextEnv, "16384")
	t.Setenv(agentMinContextEnv, "4096")
	t.Setenv(agentRAMReserveEnv, "4")
	status := contextStatus(64<<30, 12<<30)
	if status.TotalRAMGB != 64 || status.AvailableRAMGB != 12 || status.RAMReserveGB != 4 {
		t.Fatalf("unexpected RAM status: %+v", status)
	}
	if status.SelectedContextTokens != 16384 || status.MaxContextTokens != 16384 {
		t.Fatalf("context status = %+v, want selected/max 16384", status)
	}

	t.Setenv(agentMaxContextEnv, "8192")
	status = contextStatus(64<<30, 12<<30)
	if status.SelectedContextTokens != 8192 || status.MaxContextTokens != 8192 {
		t.Fatalf("configured maximum was not applied: %+v", status)
	}
	if got := outputTokenReserve(4096); got != 1024 {
		t.Fatalf("low-context output reserve = %d, want 1024", got)
	}
}

func TestPrepareAgentPromptCompactsHistoryAndKeepsSystemAndTask(t *testing.T) {
	messages := []api.Message{
		{Role: "system", Content: "Keep these system instructions."},
		{Role: "user", Content: "Implement the requested change.\n\nRepository context:\n" + strings.Repeat("repository inventory ", 1500)},
		{
			Role: "assistant",
			ToolCalls: []api.ToolCall{{
				ID:       "old-read",
				Function: api.ToolCallFunction{Name: "read_file"},
			}},
		},
		{Role: "tool", ToolCallID: "old-read", Content: strings.Repeat("stale tool output ", 3000)},
		{Role: "assistant", Content: "Earlier analysis."},
		{Role: "system", Content: "Duplicate system instructions."},
	}
	prepared, tokens, err := prepareAgentPrompt(messages, nil, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if tokens+agentOutputTokenReserve > 8192 {
		t.Fatalf("final budget %d exceeds context", tokens+agentOutputTokenReserve)
	}
	if len(prepared) > 3 || prepared[0].Role != "system" || prepared[0].Content != messages[0].Content {
		t.Fatalf("history/system compaction did not preserve core messages: %+v", prepared)
	}
	if !strings.Contains(prepared[1].Content, "Implement the requested change.") {
		t.Fatalf("current task was lost: %q", prepared[1].Content)
	}
	if strings.Contains(prepared[1].Content, "repository inventory") {
		t.Fatal("oversized repository context was not compacted")
	}
	for _, message := range prepared[2:] {
		if message.Role == "system" || message.Content == strings.Repeat("stale tool output ", 3000) {
			t.Fatalf("duplicate instructions or stale tool result survived: %+v", prepared)
		}
	}
}

func TestPrepareAgentPromptRejects21373TokenOversizeTask(t *testing.T) {
	message := api.Message{Role: "user", Content: strings.Repeat("abcd ", 21373)}
	_, estimated, err := prepareAgentPrompt([]api.Message{{Role: "system", Content: "Instructions"}, message}, nil, 16384)
	if err == nil {
		t.Fatal("oversized task unexpectedly fit context")
	}
	if estimated+outputTokenReserve(16384) <= 16384 {
		t.Fatalf("estimated budget %d should exceed selected context", estimated+outputTokenReserve(16384))
	}
	if !strings.Contains(err.Error(), "cannot fit safely") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type contextRetryChat struct {
	requests []api.ChatRequest
	errors   []error
	response api.ChatResponse
}

func (chat *contextRetryChat) Chat(_ context.Context, request *api.ChatRequest, fn api.ChatResponseFunc) error {
	chat.requests = append(chat.requests, *request)
	if len(chat.errors) > 0 {
		err := chat.errors[0]
		chat.errors = chat.errors[1:]
		if err != nil {
			return err
		}
	}
	return fn(chat.response)
}

func TestAgentRetriesHTTP400ContextErrorAtLowerFittingContext(t *testing.T) {
	t.Setenv(agentMinContextEnv, "4096")
	chat := &contextRetryChat{
		errors:   []error{errors.New(`chat request failed with status 400: {"error":"exceed_context_size_error"}`)},
		response: api.ChatResponse{Message: api.Message{Role: "assistant", Content: "ok"}},
	}
	engine := NewEngine(chat)
	_, err := engine.chatOnceWithContextStatus(context.Background(), "test", []api.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "short prompt"},
	}, true, ContextStatus{SelectedContextTokens: 16384, MaxContextTokens: 32768})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.requests) != 2 {
		t.Fatalf("requests = %d, want one bounded retry", len(chat.requests))
	}
	if chat.requests[0].Options["num_ctx"] != 16384 || chat.requests[1].Options["num_ctx"] != 8192 {
		t.Fatalf("context fallback sequence = %v, %v", chat.requests[0].Options["num_ctx"], chat.requests[1].Options["num_ctx"])
	}
	for _, request := range chat.requests {
		tokens := estimatePromptTokens(request.Messages, request.Tools)
		contextLimit := request.Options["num_ctx"].(int)
		if tokens+outputTokenReserve(contextLimit) > contextLimit {
			t.Fatalf("request budget %d exceeds num_ctx %v", tokens+outputTokenReserve(contextLimit), request.Options["num_ctx"])
		}
	}
}

func TestContextErrorDetectionAndBoundedFallbacks(t *testing.T) {
	for _, message := range []string{
		"HTTP 400: exceed_context_size_error",
		"request exceeds the available context size",
		"input length exceeds the context length",
	} {
		if !isContextSizeError(fmt.Errorf("%s", message)) {
			t.Errorf("context-size error not detected: %q", message)
		}
	}
	if isContextSizeError(errors.New("model not found")) {
		t.Fatal("unrelated error classified as context-size error")
	}
	got := lowerContextCandidates(32768, 4096)
	want := []int{16384, 8192, 4096}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("fallback contexts = %v, want %v", got, want)
	}
}
