package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ollama/ollama/api"
)

func TestEngineCompletesWithWriteFileEdit(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{
			{ID: "write-1", Function: api.ToolCallFunction{
				Name:      "write_file",
				Arguments: args(map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}\n"}),
			}},
		}}},
		{Message: api.Message{Role: "assistant", Content: "done"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "rewrite package", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || len(result.ChangedFiles) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	detected := false
	for _, event := range result.StepEvents {
		if event.Kind == StepEditDetected && event.ToolName == "write_file" {
			detected = true
		}
	}
	if !detected {
		t.Fatalf("write_file edit not detected: %+v", result.StepEvents)
	}
}

func TestEngineCompletesWithMultiEdit(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{
			{ID: "multi-1", Function: api.ToolCallFunction{
				Name: "multi_edit",
				Arguments: args(map[string]any{
					"path": "main.go",
					"edits": []any{
						map[string]any{"old_text": "package old", "new_text": "package main"},
						map[string]any{"old_text": "\n", "new_text": "\nfunc main() {}\n"},
					},
				}),
			}},
		}}},
		{Message: api.Message{Role: "assistant", Content: "done"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "rewrite package", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess {
		t.Fatalf("unexpected result: %+v", result)
	}
	b, readErr := os.ReadFile(filepath.Join(root, "main.go"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(b) != "package main\nfunc main() {}\n" {
		t.Fatalf("got %q", b)
	}
}

func TestEngineMultiEditAcceptsJSONEncodedEdits(t *testing.T) {
	root := initRepo(t)
	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{
			{ID: "multi-2", Function: api.ToolCallFunction{
				Name: "multi_edit",
				Arguments: args(map[string]any{
					"path":  "main.go",
					"edits": `[{"old_text": "package old", "new_text": "package main"}]`,
				}),
			}},
		}}},
		{Message: api.Message{Role: "assistant", Content: "done"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "rewrite package", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestAgentToolsIncludeAdvancedEditingTools(t *testing.T) {
	want := map[string]bool{"write_file": false, "list_files": false, "delete_file": false, "move_file": false, "multi_edit": false}
	for _, tool := range agentTools() {
		if _, ok := want[tool.Function.Name]; ok {
			want[tool.Function.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("agentTools missing %q", name)
		}
	}
}
