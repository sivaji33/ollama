package agent

import (
	"context"
	"testing"

	"github.com/ollama/ollama/api"
)

func TestEnginePublishesLifecycleEventsBeforeRunReturns(t *testing.T) {
	root := initRepo(t)

	chat := &scriptedChat{
		responses: []api.ChatResponse{
			{
				Message: api.Message{
					Role: "assistant",
					ToolCalls: []api.ToolCall{
						{
							ID: "edit-1",
							Function: api.ToolCallFunction{
								Name: "apply_patch",
								Arguments: args(map[string]any{
									"path":     "main.go",
									"old_text": "package old",
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
					Content: "verify the source change",
				},
			},
		},
	}

	runReturned := false
	var published []SessionEvent

	result, err := NewEngine(chat).Run(
		context.Background(),
		RunRequest{
			Model:     "test",
			Workspace: root,
			Task:      "change main.go to package main",
			MaxSteps:  4,
			Verify: []string{
				`git grep -q "package main" -- main.go`,
			},
			OnLifecycleEvent: func(event SessionEvent) error {
				if runReturned {
					t.Fatalf(
						"lifecycle event %q published after Run returned",
						event.Type,
					)
				}

				published = append(published, event)
				return nil
			},
		},
	)

	runReturned = true

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
		EventVerificationPassed,
	}

	if len(published) != len(want) {
		t.Fatalf(
			"published events = %#v, want %d events",
			published,
			len(want),
		)
	}

	for i, wantType := range want {
		if published[i].Type != wantType {
			t.Fatalf(
				"published event %d = %q, want %q; all=%#v",
				i,
				published[i].Type,
				wantType,
				published,
			)
		}
	}

	if len(result.LifecycleEvents) != len(published) {
		t.Fatalf(
			"result lifecycle count = %d, published count = %d",
			len(result.LifecycleEvents),
			len(published),
		)
	}

	for i := range published {
		if result.LifecycleEvents[i].Type != published[i].Type {
			t.Fatalf(
				"result event %d = %q, published = %q",
				i,
				result.LifecycleEvents[i].Type,
				published[i].Type,
			)
		}
	}
}
