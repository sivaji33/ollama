package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ollama/ollama/api"
	agentpkg "github.com/ollama/ollama/internal/agent"
	"github.com/ollama/ollama/llm"
)

type fakeAgentRunner struct {
	result  agentpkg.RunResult
	err     error
	request agentpkg.RunRequest
}

func TestServerChatClientPreservesParsedToolCalls(t *testing.T) {
	mock := &mockRunner{CompletionResponse: llm.CompletionResponse{
		Content:    `<tool_call>{"name":"read_file","arguments":{"path":"main.py"}}</tool_call>`,
		Done:       true,
		DoneReason: llm.DoneReasonStop,
	}}
	s := newServerWithMockRunner(t, mock)
	tmpl := `{{ if .Tools }}{{ .Tools }}{{ end }}{{ range .Messages }}{{ if .ToolCalls }}<tool_call>{{ range .ToolCalls }}{"name":"{{ .Function.Name }}","arguments":{{ .Function.Arguments }}}{{ end }}</tool_call>{{ else }}{{ .Role }}: {{ .Content }}{{ end }}{{ end }}`
	createMinimalGGUFModel(t, s, "agent-adapter-test", nil, tmpl, nil)

	stream := false
	args := api.NewToolPropertiesMap()
	args.Set("path", api.ToolProperty{Type: api.PropertyType{"string"}})
	req := &api.ChatRequest{
		Model: "agent-adapter-test", Stream: &stream,
		Messages: []api.Message{{Role: "user", Content: "read main.py"}},
		Tools: api.Tools{{Type: "function", Function: api.ToolFunction{
			Name: "read_file", Parameters: api.ToolFunctionParameters{Type: "object", Properties: args, Required: []string{"path"}},
		}}},
	}
	var got api.ChatResponse
	if err := (serverChatClient{server: s}).Chat(t.Context(), req, func(response api.ChatResponse) error { got = response; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", got.Message.ToolCalls)
	}
	call := got.Message.ToolCalls[0]
	if call.ID == "" || call.Function.Name != "read_file" {
		t.Fatalf("tool call identity not preserved: %+v", call)
	}
	path, ok := call.Function.Arguments.Get("path")
	if !ok || path != "main.py" {
		t.Fatalf("tool arguments not preserved: %+v", call.Function.Arguments.ToMap())
	}
}

func (f *fakeAgentRunner) Run(_ context.Context, request agentpkg.RunRequest) (agentpkg.RunResult, error) {
	f.request = request
	return f.result, f.err
}

func TestAgentRunHandlerReturnsStructuredResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAgentRunner{result: agentpkg.RunResult{SessionID: "session-1", Status: agentpkg.StatusSuccess, ChangedFiles: []string{"main.go"}}}
	s := &Server{agentRunner: fake}
	req := httptest.NewRequest(http.MethodPost, "/api/agent/run", strings.NewReader(`{"model":"test","workspace":"C:\\repo","task":"edit it"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	s.AgentRunHandler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got agentpkg.RunResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "session-1" || fake.request.Task != "edit it" {
		t.Fatalf("unexpected response/request: %+v %+v", got, fake.request)
	}
}

func TestAgentRunHandlerRejectsMissingFields(t *testing.T) {
	s := &Server{agentRunner: &fakeAgentRunner{}}
	req := httptest.NewRequest(http.MethodPost, "/api/agent/run", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	s.AgentRunHandler(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}
