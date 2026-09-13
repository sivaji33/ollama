package agent

import (
	"context"
	"os"
	"path/filepath"
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
				if event.Type == EventEditingStarted {
					contents, err := os.ReadFile(filepath.Join(root, "main.go"))
					if err != nil {
						t.Fatalf("read source during editing event: %v", err)
					}
					if string(contents) != "package old\n" {
						t.Fatalf("editing event arrived after tool completed: %q", contents)
					}
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
		EventEditingStarted,
		EventDiffDetected,
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

func TestEngineDoesNotPublishDiffDetectedWithoutMeaningfulRealDiff(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{
			Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
				ID: "failed-edit",
				Function: api.ToolCallFunction{
					Name: "apply_patch",
					Arguments: args(map[string]any{
						"path":     "main.go",
						"old_text": "package missing",
						"new_text": "package main",
					}),
				},
			}}},
		},
		{Message: api.Message{Role: "assistant", Content: "done"}},
	}}

	var published []SessionEventType
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{
		Model:     "test",
		Workspace: root,
		Task:      "change main.go",
		MaxSteps:  2,
		OnLifecycleEvent: func(event SessionEvent) error {
			published = append(published, event.Type)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status == StatusSuccess {
		t.Fatalf("failed edit reported success: %+v", result)
	}
	for _, eventType := range published {
		if eventType == EventDiffDetected {
			t.Fatalf("diff_detected published without meaningful real diff: %v", published)
		}
		if eventType == EventVerificationPassed {
			t.Fatalf("verification passed without meaningful real diff: %v", published)
		}
	}
}
