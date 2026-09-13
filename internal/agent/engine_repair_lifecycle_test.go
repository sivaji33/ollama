package agent

import (
	"context"
	"testing"

	"github.com/ollama/ollama/api"
)

func TestEngineEmitsRepairReverificationLifecycle(t *testing.T) {
	root := initRepo(t)

	chat := &scriptedChat{
		responses: []api.ChatResponse{
			{
				Message: api.Message{
					Role: "assistant",
					ToolCalls: []api.ToolCall{
						{
							ID: "break-1",
							Function: api.ToolCallFunction{
								Name: "apply_patch",
								Arguments: args(map[string]any{
									"path":     "main.go",
									"old_text": "package old",
									"new_text": "package broken",
								}),
							},
						},
					},
				},
			},
			{
				Message: api.Message{
					Role:    "assistant",
					Content: "verify the change",
				},
			},
			{
				Message: api.Message{
					Role: "assistant",
					ToolCalls: []api.ToolCall{
						{
							ID: "repair-1",
							Function: api.ToolCallFunction{
								Name: "apply_patch",
								Arguments: args(map[string]any{
									"path":     "main.go",
									"old_text": "package broken",
									"new_text": "package main",
								}),
							},
						},
					},
				},
			},
			{
				Message: api.Message{
					Role:    "assistant",
					Content: "repair complete",
				},
			},
		},
	}

	result, err := NewEngine(chat).Run(
		context.Background(),
		RunRequest{
			Model:     "test",
			Workspace: root,
			Task:      "change main.go to package main",
			MaxSteps:  6,
			Verify: []string{
				`git grep -q "package main" -- main.go`,
			},
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Status != StatusSuccess {
		t.Fatalf(
			"status = %q, want %q; result=%+v",
			result.Status,
			StatusSuccess,
			result,
		)
	}

	want := []SessionEventType{
		EventVerificationStart,
		EventVerificationFailed,
		EventRepairStarted,
		EventRepairCompleted,
		EventVerificationStart,
		EventVerificationPassed,
	}

	if len(result.LifecycleEvents) != len(want) {
		t.Fatalf(
			"lifecycle events = %#v, want %d events",
			result.LifecycleEvents,
			len(want),
		)
	}

	for i, wantType := range want {
		if result.LifecycleEvents[i].Type != wantType {
			t.Fatalf(
				"lifecycle event %d = %q, want %q; all=%#v",
				i,
				result.LifecycleEvents[i].Type,
				wantType,
				result.LifecycleEvents,
			)
		}
	}

	if len(result.VerificationResults) == 0 {
		t.Fatalf("final verification results were empty")
	}

	if !result.VerificationResults[len(result.VerificationResults)-1].Passed {
		t.Fatalf(
			"final verification did not pass: %#v",
			result.VerificationResults,
		)
	}
}
