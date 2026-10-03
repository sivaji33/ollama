package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ollama/ollama/internal/agent"
	"github.com/ollama/ollama/internal/selfimprove"
)

type selfDevelopmentRequest struct {
	Workspace string   `json:"workspace"`
	Task      string   `json:"task"`
	Verify    []string `json:"verify"`
	Build     string   `json:"build"`
}

type selfDevelopmentSnapshot struct {
	ID        string                         `json:"id"`
	State     string                         `json:"state"`
	Workspace string                         `json:"workspace"`
	Task      string                         `json:"task"`
	Runtime   selfimprove.RuntimeDiagnostic  `json:"runtime"`
	Events    []selfimprove.TransactionEvent `json:"events"`
	Result    *selfimprove.SessionResult     `json:"result,omitempty"`
	Error     string                         `json:"error,omitempty"`
}

type selfDevelopmentRun struct {
	mu     sync.RWMutex
	state  selfDevelopmentSnapshot
	cancel context.CancelFunc
}

func (r *selfDevelopmentRun) append(event selfimprove.TransactionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Events = append(r.state.Events, event)
	if event.Status == "EXECUTING" {
		r.state.State = "EXECUTING"
	}
	if event.Status == "FAILED" {
		r.state.State = "FAILED"
	}
	if event.Stage == "rollback" && event.Status == "COMPLETED" {
		r.state.State = "ROLLED BACK"
	}
}

func (r *selfDevelopmentRun) snapshot() selfDevelopmentSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := r.state
	snapshot.Events = append([]selfimprove.TransactionEvent(nil), r.state.Events...)
	return snapshot
}

func (s *Server) SelfDevelopmentStartHandler(c *gin.Context) {
	if !selfDevelopmentRequestIsLocal(c.Request) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "self-development transactions are restricted to local requests"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var request selfDevelopmentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(request.Task) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "task is required"})
		return
	}
	if strings.TrimSpace(request.Workspace) == "" {
		workspace, err := os.Getwd()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("resolve default workspace: %v", err)})
			return
		}
		request.Workspace = workspace
	}
	workspace, err := filepath.Abs(request.Workspace)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("resolve workspace: %v", err)})
		return
	}
	if len(request.Verify) == 0 {
		request.Verify = []string{"go build ./..."}
	}
	for i, command := range request.Verify {
		if strings.TrimSpace(command) == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("verification command %d is empty", i+1)})
			return
		}
	}

	id := "selfup_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	runContext, cancel := context.WithCancel(context.Background())
	run := &selfDevelopmentRun{
		cancel: cancel,
		state: selfDevelopmentSnapshot{
			ID:        id,
			State:     "PLANNED",
			Workspace: workspace,
			Task:      request.Task,
			Runtime: selfimprove.RuntimeDiagnostic{
				Executable:   selfimprove.CustomRuntimeExecutable,
				Port:         "127.0.0.1:11435",
				Endpoint:     selfimprove.CustomRuntimeEndpoint,
				Model:        selfimprove.CustomRuntimeModel,
				ProcessState: "verification pending",
			},
			Events: []selfimprove.TransactionEvent{{
				ID: id, Stage: "transaction", Status: "PLANNED",
				Message:   "self-development transaction accepted",
				Timestamp: time.Now().UTC(),
			}},
		},
	}
	s.selfDevelopmentMu.Lock()
	if s.selfDevelopmentRuns == nil {
		s.selfDevelopmentRuns = make(map[string]*selfDevelopmentRun)
	}
	s.selfDevelopmentRuns[id] = run
	s.selfDevelopmentMu.Unlock()

	go func() {
		run.append(selfimprove.TransactionEvent{
			ID: id, Stage: "runtime", Status: "EXECUTING",
			Message:   "verifying only the configured local executable, endpoint, model and inference",
			Timestamp: time.Now().UTC(),
		})
		client, runtimeDiagnostic, runtimeErr := selfimprove.VerifyCustomRuntime(runContext)
		run.mu.Lock()
		run.state.Runtime = runtimeDiagnostic
		run.mu.Unlock()
		if runtimeErr != nil {
			run.mu.Lock()
			run.state.State = "FAILED"
			run.state.Error = runtimeErr.Error()
			run.state.Events = append(run.state.Events, selfimprove.TransactionEvent{
				ID: id, Stage: "runtime", Status: "FAILED",
				Message: runtimeErr.Error(), Timestamp: time.Now().UTC(),
			})
			run.mu.Unlock()
			return
		}
		run.append(selfimprove.TransactionEvent{
			ID: id, Stage: "runtime", Status: "COMPLETED",
			Message:   "configured executable, process, endpoint, model and inference verified",
			Timestamp: time.Now().UTC(),
		})
		config := selfimprove.Config{
			TransactionID: id,
			Workspace:     workspace,
			Model:         selfimprove.CustomRuntimeModel,
			Verify:        request.Verify,
			Topics:        []string{request.Task},
			Cycles:        1,
			Build:         request.Build,
			OnEvent: func(event selfimprove.TransactionEvent) error {
				run.append(event)
				return nil
			},
			OnAgentEvent: func(event agent.SessionEvent) error {
				stage, status, message := agentProgress(event)
				run.append(selfimprove.TransactionEvent{
					ID: id, Stage: stage, Status: status, ToolName: event.ToolName,
					Path: event.Path, Diff: event.FilesystemDiff,
					Message: message, Timestamp: event.Timestamp,
				})
				return nil
			},
		}
		result, runErr := selfimprove.Run(runContext, config, agent.NewEngine(client))
		run.mu.Lock()
		defer run.mu.Unlock()
		run.state.Result = &result
		switch {
		case runErr != nil:
			run.state.State = "FAILED"
			run.state.Error = runErr.Error()
		case result.Kept > 0:
			run.state.State = "COMPLETED"
		case result.Reverted > 0:
			run.state.State = "ROLLED BACK"
		default:
			run.state.State = "FAILED"
			run.state.Error = "transaction made no verified live change"
		}
		run.state.Events = append(run.state.Events, selfimprove.TransactionEvent{
			ID: id, Stage: "transaction", Status: run.state.State,
			Message: run.state.Error, Timestamp: time.Now().UTC(),
		})
	}()

	c.JSON(http.StatusAccepted, run.snapshot())
}

func (s *Server) SelfDevelopmentGetHandler(c *gin.Context) {
	if !selfDevelopmentRequestIsLocal(c.Request) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "self-development transactions are restricted to local requests"})
		return
	}
	run := s.selfDevelopmentRun(c.Param("id"))
	if run == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "self-development transaction not found"})
		return
	}
	c.JSON(http.StatusOK, run.snapshot())
}

func (s *Server) SelfDevelopmentCancelHandler(c *gin.Context) {
	if !selfDevelopmentRequestIsLocal(c.Request) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "self-development transactions are restricted to local requests"})
		return
	}
	run := s.selfDevelopmentRun(c.Param("id"))
	if run == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "self-development transaction not found"})
		return
	}
	run.mu.RLock()
	cancel := run.cancel
	state := run.state.State
	run.mu.RUnlock()
	if state != "PLANNED" && state != "EXECUTING" {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "transaction is not active"})
		return
	}
	cancel()
	c.JSON(http.StatusAccepted, run.snapshot())
}

func (s *Server) selfDevelopmentRun(id string) *selfDevelopmentRun {
	s.selfDevelopmentMu.RLock()
	defer s.selfDevelopmentMu.RUnlock()
	return s.selfDevelopmentRuns[id]
}

func selfDevelopmentRequestIsLocal(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return false
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func agentProgress(event agent.SessionEvent) (string, string, string) {
	switch event.Type {
	case agent.EventToolCall:
		if isAgentInspectionTool(event.ToolName) {
			return "inspection", "EXECUTING", "Inspecting " + event.Path
		}
		if isAgentFileMutationTool(event.ToolName) {
			return "files", "EXECUTING", "Applying " + event.ToolName + " to " + event.Path
		}
		return "agent", "EXECUTING", event.Message
	case agent.EventToolResult:
		if strings.HasSuffix(event.Message, "failed") {
			if isAgentInspectionTool(event.ToolName) {
				return "inspection", "FAILED", event.Message
			}
			if isAgentFileMutationTool(event.ToolName) {
				return "files", "FAILED", event.Message
			}
			return "agent", "FAILED", event.Message
		}
		if isAgentInspectionTool(event.ToolName) {
			return "inspection", "COMPLETED", "Inspected " + event.Path
		}
		if isAgentFileMutationTool(event.ToolName) {
			return "files", "COMPLETED", event.Message
		}
		return "agent", "COMPLETED", event.Message
	case agent.EventDiffDetected:
		return "files", "COMPLETED", event.Message
	case agent.EventFilesystemChange:
		return "files", "EXECUTING", event.Message
	case agent.EventVerificationStart:
		return "verification", "EXECUTING", event.Message
	case agent.EventVerificationFailed:
		return "verification", "FAILED", event.Message
	case agent.EventVerificationPassed:
		return "verification", "COMPLETED", event.Message
	default:
		if event.Message == "" {
			return "agent", "EXECUTING", string(event.Type)
		}
		return "agent", "EXECUTING", event.Message
	}
}

func isAgentInspectionTool(name string) bool {
	switch name {
	case "search_files", "list_files", "read_file":
		return true
	default:
		return false
	}
}

func isAgentFileMutationTool(name string) bool {
	switch name {
	case "apply_patch", "write_file", "multi_edit", "move_file", "delete_file":
		return true
	default:
		return false
	}
}
