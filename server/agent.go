package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ollama/ollama/api"
	agentpkg "github.com/ollama/ollama/internal/agent"
)

type agentRunner interface {
	Run(context.Context, agentpkg.RunRequest) (agentpkg.RunResult, error)
}

func (s *Server) AgentRunHandler(c *gin.Context) {
	var request agentpkg.RunRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Workspace) == "" || strings.TrimSpace(request.Task) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "model, workspace, and task are required"})
		return
	}
	runner := s.agentRunner
	if runner == nil {
		runner = agentpkg.NewEngine(serverChatClient{server: s})
	}
	result, err := runner.Run(c.Request.Context(), request)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "result": result})
		return
	}
	c.JSON(http.StatusOK, result)
}

// serverChatClient deliberately routes agent turns through the existing chat
// handler so scheduling, templates, and tool-call parsing remain single-source.
type serverChatClient struct{ server *Server }

func (client serverChatClient) Chat(ctx context.Context, request *api.ChatRequest, fn api.ChatResponseFunc) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body)).WithContext(ctx)
	httpRequest.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httpRequest
	client.server.ChatHandler(ginContext)
	if recorder.Code < 200 || recorder.Code >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
		if payload.Error == "" {
			payload.Error = recorder.Body.String()
		}
		return fmt.Errorf("chat request failed with status %d: %s", recorder.Code, payload.Error)
	}
	decoder := json.NewDecoder(recorder.Body)
	called := false
	for {
		var response api.ChatResponse
		if err := decoder.Decode(&response); errors.Is(err, context.Canceled) {
			return err
		} else if err != nil {
			if errors.Is(err, io.EOF) && called {
				break
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if called && strings.TrimSpace(recorder.Body.String()) != "" {
				break
			}
			return err
		}
		called = true
		if err := fn(response); err != nil {
			return err
		}
		if response.Done {
			break
		}
	}
	return nil
}
