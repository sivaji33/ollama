package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
)

func resetAgentLimitEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		agentMaxTurnsEnv,
		agentToolCallsPerTurnEnv,
		agentStagnantTurnsEnv,
		agentContextWindowEnv,
		agentConversationMessagesEnv,
		agentObservationBytesEnv,
		agentCommandTimeoutEnv,
		agentOutputLimitEnv,
	} {
		t.Setenv(name, "")
	}
}

func TestAgentLimitsDefaultToUnrestrictedOperations(t *testing.T) {
	resetAgentLimitEnv(t)

	if got := agentTurnLimit(); got != 0 {
		t.Fatalf("agentTurnLimit() = %d, want 0 (unlimited)", got)
	}
	if got := agentToolCallsPerTurnLimit(); got != 0 {
		t.Fatalf("agentToolCallsPerTurnLimit() = %d, want 0 (unlimited)", got)
	}
	if got := agentConversationLimit(); got != 0 {
		t.Fatalf("agentConversationLimit() = %d, want 0 (unlimited)", got)
	}
	if got := agentObservationLimit(); got != 0 {
		t.Fatalf("agentObservationLimit() = %d, want 0 (unlimited)", got)
	}
	if got := agentCommandTimeout(); got >= 0 {
		t.Fatalf("agentCommandTimeout() = %v, want no timeout", got)
	}
	if got := agentOutputLimit(); got >= 0 {
		t.Fatalf("agentOutputLimit() = %d, want unlimited output", got)
	}
	if got := agentStagnationLimit(); got != defaultAgentStagnantTurns {
		t.Fatalf("agentStagnationLimit() = %d, want %d", got, defaultAgentStagnantTurns)
	}
	if got := agentContextWindowLimit(); got != defaultAgentContextWindow {
		t.Fatalf("agentContextWindowLimit() = %d, want %d", got, defaultAgentContextWindow)
	}
}

func TestAgentLimitsReadOperatorEnvironment(t *testing.T) {
	resetAgentLimitEnv(t)
	t.Setenv(agentMaxTurnsEnv, "7")
	t.Setenv(agentToolCallsPerTurnEnv, "3")
	t.Setenv(agentStagnantTurnsEnv, "9")
	t.Setenv(agentContextWindowEnv, "32768")
	t.Setenv(agentConversationMessagesEnv, "40")
	t.Setenv(agentObservationBytesEnv, "8192")
	t.Setenv(agentCommandTimeoutEnv, "600")
	t.Setenv(agentOutputLimitEnv, "1048576")

	if got := agentTurnLimit(); got != 7 {
		t.Fatalf("agentTurnLimit() = %d, want 7", got)
	}
	if got := agentToolCallsPerTurnLimit(); got != 3 {
		t.Fatalf("agentToolCallsPerTurnLimit() = %d, want 3", got)
	}
	if got := agentStagnationLimit(); got != 9 {
		t.Fatalf("agentStagnationLimit() = %d, want 9", got)
	}
	if got := agentContextWindowLimit(); got != 32768 {
		t.Fatalf("agentContextWindowLimit() = %d, want 32768", got)
	}
	if got := agentConversationLimit(); got != 40 {
		t.Fatalf("agentConversationLimit() = %d, want 40", got)
	}
	if got := agentObservationLimit(); got != 8192 {
		t.Fatalf("agentObservationLimit() = %d, want 8192", got)
	}
	if got := agentCommandTimeout(); got != 10*time.Minute {
		t.Fatalf("agentCommandTimeout() = %v, want 10m", got)
	}
	if got := agentOutputLimit(); got != 1048576 {
		t.Fatalf("agentOutputLimit() = %d, want 1048576", got)
	}
}

func listFilesCalls(count int) []api.ToolCall {
	calls := make([]api.ToolCall, 0, count)
	for i := 0; i < count; i++ {
		calls = append(calls, api.ToolCall{
			ID: fmt.Sprintf("list-%d", i+1),
			Function: api.ToolCallFunction{
				Name:      "list_files",
				Arguments: args(map[string]any{"pattern": "*"}),
			},
		})
	}
	return calls
}

func TestEngineExecutesEveryToolCallByDefault(t *testing.T) {
	resetAgentLimitEnv(t)
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: listFilesCalls(8)}},
		patchCall(),
		{Message: api.Message{Role: "assistant", Content: "completed"}},
	}}

	result, err := NewEngine(chat).Run(context.Background(), RunRequest{
		Model: "test", Workspace: root, Task: "change package", Verify: []string{"git diff --check"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess {
		t.Fatalf("status = %q, final summary = %q", result.Status, result.FinalSummary)
	}
	listCount := 0
	for _, record := range result.ToolCalls {
		if strings.Contains(record.Error, "budget") {
			t.Fatalf("default run refused a tool call: %+v", record)
		}
		if record.Name == "list_files" {
			listCount++
		}
	}
	if listCount != 8 {
		t.Fatalf("executed list_files calls = %d, want 8", listCount)
	}
}

func TestEngineToolCallBudgetAppliesOnlyWhenOperatorSetsOne(t *testing.T) {
	resetAgentLimitEnv(t)
	t.Setenv(agentToolCallsPerTurnEnv, "2")
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: listFilesCalls(4)}},
		patchCall(),
		{Message: api.Message{Role: "assistant", Content: "completed"}},
	}}

	result, err := NewEngine(chat).Run(context.Background(), RunRequest{
		Model: "test", Workspace: root, Task: "change package", Verify: []string{"git diff --check"},
	})
	if err != nil {
		t.Fatal(err)
	}
	executed, refused := 0, 0
	for _, record := range result.ToolCalls {
		switch {
		case strings.Contains(record.Error, "budget exceeded"):
			refused++
		case record.Name == "list_files":
			executed++
		}
	}
	if executed != 2 || refused != 2 {
		t.Fatalf("executed = %d refused = %d, want 2 and 2: %+v", executed, refused, result.ToolCalls)
	}
}
