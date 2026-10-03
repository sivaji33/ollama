package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ollama/ollama/api"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
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

func TestEngineEditsLiveFilesystemWithoutGitForSelfDevelopment(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := agenttools.NewFilesystemCheckpoint(root)
	if err != nil {
		t.Fatal(err)
	}
	defer checkpoint.Close()

	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
			ID: "write-live",
			Function: api.ToolCallFunction{
				Name:      "write_file",
				Arguments: args(map[string]any{"path": "main.go", "content": "package main\n"}),
			},
		}}}},
		{Message: api.Message{Role: "assistant", Content: "changed the live source"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{
		Model:                "test",
		Workspace:            root,
		Task:                 "change main.go",
		FilesystemCheckpoint: checkpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess {
		t.Fatalf("status = %s; summary=%q", result.Status, result.FinalSummary)
	}
	contents, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "package main\n" {
		t.Fatalf("live source = %q, want modified content", contents)
	}
	if !strings.Contains(result.GitDiff, "-package old") || !strings.Contains(result.GitDiff, "+package main") {
		t.Fatalf("engine did not report the actual filesystem diff: %q", result.GitDiff)
	}
	for _, tool := range chat.requests[0].Tools {
		if tool.Function.Name == "shell" {
			t.Fatal("self-development agent was given arbitrary shell access")
		}
	}
}

func TestSelfDevelopmentContinuesUntilAllRequestedFileOperationsFinish(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"main.go":   "package old\n",
		"notes.txt": "remove after source edit\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint, err := agenttools.NewFilesystemCheckpoint(root)
	if err != nil {
		t.Fatal(err)
	}
	defer checkpoint.Close()

	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
			ID: "edit-source",
			Function: api.ToolCallFunction{
				Name:      "apply_patch",
				Arguments: args(map[string]any{"path": "main.go", "old_text": "package old", "new_text": "package main"}),
			},
		}}}},
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
			ID: "delete-note",
			Function: api.ToolCallFunction{
				Name:      "delete_file",
				Arguments: args(map[string]any{"path": "notes.txt"}),
			},
		}}}},
		{Message: api.Message{Role: "assistant", Content: "completed both requested file operations"}},
	}}
	result, err := NewEngine(chat).Run(context.Background(), RunRequest{
		Model:                "test",
		Workspace:            root,
		Task:                 "change the source and delete the note",
		FilesystemCheckpoint: checkpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || len(chat.requests) != 3 {
		t.Fatalf("status=%s turns=%d summary=%q; want success after all operations", result.Status, len(chat.requests), result.FinalSummary)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("requested deletion was not applied: %v", err)
	}
	diff, err := checkpoint.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Files) != 2 {
		t.Fatalf("final filesystem diff = %+v; want both source modification and deletion", diff.Files)
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
