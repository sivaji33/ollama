package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ollama/ollama/api"
)

var errMuseStopAfterDenial = errors.New("mock chat stopped after denial")

type museDeniedEditChat struct {
	calls int
}

func (c *museDeniedEditChat) Chat(
	_ context.Context,
	_ *api.ChatRequest,
	fn api.ChatResponseFunc,
) error {
	c.calls++

	if c.calls > 1 {
		return errMuseStopAfterDenial
	}

	arguments := api.NewToolCallFunctionArguments()
	arguments.Set("path", "main.go")
	arguments.Set("old_text", "package old")
	arguments.Set("new_text", "package main")

	return fn(api.ChatResponse{
		Message: api.Message{
			Role: "assistant",
			ToolCalls: []api.ToolCall{
				{
					ID: "denied-edit",
					Function: api.ToolCallFunction{
						Name:      "apply_patch",
						Arguments: arguments,
					},
				},
			},
		},
	})
}

func TestMUSEEngineDeniedEditPreservesFile(t *testing.T) {
	root := initRepo(t)
	target := filepath.Join(root, "main.go")

	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(original), "package old") {
		t.Fatalf("unexpected original source: %q", original)
	}

	chat := &museDeniedEditChat{}
	approvalCalls := 0
	editingStartedWithoutApproval := false

	result, runErr := NewEngine(chat).Run(
		context.Background(),
		RunRequest{
			SessionID:        "muse-denial-test",
			Model:            "test",
			Workspace:        root,
			Task:             "change package old to package main",
			EnforceApprovals: true,
			OnLifecycleEvent: func(event SessionEvent) error {
				if event.Type == EventEditingStarted {
					editingStartedWithoutApproval = true
				}
				return nil
			},
			ApproveTool: func(
				_ context.Context,
				approval ToolApprovalRequest,
			) (bool, error) {
				approvalCalls++

				if approval.ToolName != "apply_patch" {
					t.Fatalf("unexpected tool: %s", approval.ToolName)
				}

				before, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}

				if string(before) != string(original) {
					t.Fatal("file changed before approval decision")
				}

				return false, nil
			},
		},
	)

	if approvalCalls != 1 {
		t.Fatalf(
			"approval calls=%d, want 1; run error=%v; contextBuilt=%v; steps=%d; calls=%+v",
			approvalCalls,
			runErr,
			result.ContextBuilt,
			result.StepsExecuted,
			result.ToolCalls,
		)
	}

	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	if string(after) != string(original) {
		t.Fatalf(
			"SECURITY FAILURE: denied operation modified file: %q",
			after,
		)
	}

	if editingStartedWithoutApproval {
		t.Fatal("EDITING event emitted despite owner denial")
	}
	deniedRecorded := false

	for _, call := range result.ToolCalls {
		if call.Name == "apply_patch" &&
			strings.Contains(call.Error, "owner denied apply_patch") {
			deniedRecorded = true
		}
	}

	if !deniedRecorded {
		t.Fatalf(
			"missing denied tool record: %+v; run error=%v",
			result.ToolCalls,
			runErr,
		)
	}

	t.Log("PASS: approval callback invoked")
	t.Log("PASS: owner denial recorded")
	t.Log("PASS: original file unchanged")
}
