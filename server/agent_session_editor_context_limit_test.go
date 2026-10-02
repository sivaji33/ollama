package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditorContextUnrestrictedByDefault(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_EDITOR_CONTEXT_BYTES", "")

	text := strings.Repeat("x", 96*1024)
	payload := json.RawMessage(`{"selection":{"text":"` + text + `"}}`)
	task := taskWithEditorContext("fix the selection", payload)
	if !strings.Contains(task, text) {
		t.Fatal("default editor context was dropped")
	}
	if strings.Contains(task, "was dropped") {
		t.Fatal("default editor context was replaced with a drop marker")
	}
}

func TestEditorContextRespectsOperatorLimit(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_EDITOR_CONTEXT_BYTES", "1024")

	payload := json.RawMessage(`{"selection":{"text":"` + strings.Repeat("x", 4096) + `"}}`)
	task := taskWithEditorContext("fix the selection", payload)
	if !strings.Contains(task, "exceeded 1024 bytes and was dropped") {
		t.Fatalf("limited editor context was not dropped: %q", task)
	}
	if !strings.Contains(task, "[CURRENT EDITOR CONTEXT]") {
		t.Fatal("editor context section missing")
	}
}
