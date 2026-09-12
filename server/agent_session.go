package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	agentpkg "github.com/ollama/ollama/internal/agent"
)

type sessionStore interface {
	Save(agentpkg.SessionSnapshot) error
	Load(string) (agentpkg.SessionSnapshot, error)
	AppendEvent(string, agentpkg.SessionEvent) error
	Events(string) ([]agentpkg.SessionEvent, error)
	SaveDiff(string, string) error
	LoadDiff(string) (string, error)
}

func (s *Server) resolveAgentSessionStore() (sessionStore, error) {
	if s.agentSessionStore != nil {
		return s.agentSessionStore, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	return agentpkg.NewSessionStore(
		filepath.Join(home, ".ollama", "agent", "sessions"),
	)
}

func (s *Server) AgentSessionCreateHandler(c *gin.Context) {
	var request agentpkg.RunRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		c.AbortWithStatusJSON(
			http.StatusBadRequest,
			gin.H{"error": err.Error()},
		)
		return
	}

	if strings.TrimSpace(request.Workspace) == "" ||
		strings.TrimSpace(request.Task) == "" {
		c.AbortWithStatusJSON(
			http.StatusBadRequest,
			gin.H{"error": "workspace and task are required"},
		)
		return
	}

	if strings.TrimSpace(request.Model) == "" {
		request.Model = agentpkg.DefaultAgentModel
	}

	runner := s.agentRunner
	if runner == nil {
		runner = agentpkg.NewEngine(serverChatClient{server: s})
	}

	started := time.Now().UTC()

	result, runErr := runner.Run(c.Request.Context(), request)

	if strings.TrimSpace(result.SessionID) == "" {
		if runErr != nil {
			c.JSON(
				http.StatusInternalServerError,
				gin.H{"error": runErr.Error()},
			)
			return
		}

		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": "agent returned an empty session id"},
		)
		return
	}

	store, err := s.resolveAgentSessionStore()
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	state := agentpkg.SessionStateFailed
	if result.Status == agentpkg.StatusSuccess {
		state = agentpkg.SessionStateVerified
	}

	finished := time.Now().UTC()

	snapshot := agentpkg.SessionSnapshot{
		ID:                  result.SessionID,
		Model:               request.Model,
		Workspace:           request.Workspace,
		Task:                request.Task,
		State:               state,
		CreatedAt:           started,
		UpdatedAt:           finished,
		StepsExecuted:       result.StepsExecuted,
		ChangedFiles:        result.ChangedFiles,
		VerificationResults: result.VerificationResults,
		FinalSummary:        result.FinalSummary,
	}

	if err := store.Save(snapshot); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	if err := store.AppendEvent(
		snapshot.ID,
		agentpkg.SessionEvent{
			Type:      agentpkg.EventSessionCreated,
			Timestamp: started,
			Message:   "session created",
		},
	); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	if result.GitDiff != "" {
		if err := store.SaveDiff(snapshot.ID, result.GitDiff); err != nil {
			c.JSON(
				http.StatusInternalServerError,
				gin.H{"error": err.Error()},
			)
			return
		}
	}

	finalEvent := agentpkg.EventFailed
	finalMessage := "session failed"

	if state == agentpkg.SessionStateVerified {
		finalEvent = agentpkg.EventCompleted
		finalMessage = "session completed"
	}

	if err := store.AppendEvent(
		snapshot.ID,
		agentpkg.SessionEvent{
			Type:      finalEvent,
			Timestamp: finished,
			Message:   finalMessage,
		},
	); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	if runErr != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{
				"error":  runErr.Error(),
				"result": result,
			},
		)
		return
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) AgentSessionGetHandler(c *gin.Context) {
	store, err := s.resolveAgentSessionStore()
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	snapshot, err := store.Load(c.Param("id"))
	if err != nil {
		c.JSON(
			http.StatusNotFound,
			gin.H{"error": err.Error()},
		)
		return
	}

	c.JSON(http.StatusOK, snapshot)
}

func (s *Server) AgentSessionEventsHandler(c *gin.Context) {
	store, err := s.resolveAgentSessionStore()
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	events, err := store.Events(c.Param("id"))
	if err != nil {
		c.JSON(
			http.StatusNotFound,
			gin.H{"error": err.Error()},
		)
		return
	}

	c.JSON(http.StatusOK, events)
}

func (s *Server) AgentSessionDiffHandler(c *gin.Context) {
	store, err := s.resolveAgentSessionStore()
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	diff, err := store.LoadDiff(c.Param("id"))
	if err != nil {
		c.JSON(
			http.StatusNotFound,
			gin.H{"error": err.Error()},
		)
		return
	}

	c.JSON(
		http.StatusOK,
		gin.H{"diff": diff},
	)
}
