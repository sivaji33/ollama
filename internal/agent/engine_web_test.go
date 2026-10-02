package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ollama/ollama/api"
)

// TestEngineGathersInternetKnowledgeBeforeEditing proves the agent can search
// the internet for knowledge, receive the results as a tool observation, and
// then apply and verify a repository change in the same session.
func TestEngineGathersInternetKnowledgeBeforeEditing(t *testing.T) {
	root := initRepo(t)

	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.PostFormValue("q"))
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><div class="result">
<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2Feffective_go">Effective Go</a>
<a class="result__snippet">Tips for writing clear, idiomatic Go code.</a>
</div></body></html>`)
	}))
	defer server.Close()
	t.Setenv("OLLAMA_AGENT_WEB_SEARCH_URL", server.URL)

	chat := &scriptedChat{responses: []api.ChatResponse{
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{
			{ID: "search-1", Function: api.ToolCallFunction{
				Name:      "web_search",
				Arguments: args(map[string]any{"query": "effective go", "max_results": 3}),
			}},
		}}},
		{Message: api.Message{Role: "assistant", ToolCalls: []api.ToolCall{
			{ID: "patch-1", Function: api.ToolCallFunction{
				Name:      "apply_patch",
				Arguments: args(map[string]any{"path": "main.go", "old_text": "package old", "new_text": "package main"}),
			}},
		}}},
	}}

	result, err := NewEngine(chat).Run(context.Background(), RunRequest{Model: "test", Workspace: root, Task: "change package", Verify: []string{"git diff --check"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(queries) != 1 || queries[0] != "effective go" {
		t.Fatalf("search queries = %v, want one query for %q", queries, "effective go")
	}
	if len(result.ToolCalls) < 2 || result.ToolCalls[0].Name != "web_search" {
		t.Fatalf("tool calls = %+v, want web_search first", result.ToolCalls)
	}
	if !strings.Contains(result.ToolCalls[0].Output, "https://go.dev/doc/effective_go") {
		t.Fatalf("search output missing decoded URL: %q", result.ToolCalls[0].Output)
	}

	// The internet knowledge must reach the model as a tool observation before
	// the editing turn so the change can be grounded in it.
	if len(chat.requests) < 2 {
		t.Fatalf("model requests = %d, want at least 2", len(chat.requests))
	}
	followup := chat.requests[1].Messages
	last := followup[len(followup)-1]
	if last.Role != "tool" || last.ToolName != "web_search" {
		t.Fatalf("last followup message = %+v, want web_search tool observation", last)
	}
	if !strings.Contains(last.Content, "Effective Go") {
		t.Fatalf("observation missing internet knowledge: %q", last.Content)
	}
}
