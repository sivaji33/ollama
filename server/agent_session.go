package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	agentpkg "github.com/ollama/ollama/internal/agent"
)

type sessionStore interface {
	Save(agentpkg.SessionSnapshot) error
	Load(string) (agentpkg.SessionSnapshot, error)
	AppendEvent(string, agentpkg.SessionEvent) error
	Events(string) ([]agentpkg.SessionEvent, error)
	SaveDiff(string, string) error
	LoadDiff(string) (string, error)
	RecoverInterruptedSessions(time.Time) error
}

func (s *Server) resolveAgentSessionStore() (sessionStore, error) {
	store := s.agentSessionStore
	if store == nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		store, err = agentpkg.NewSessionStore(
			filepath.Join(home, ".ollama", "agent", "sessions"),
		)
		if err != nil {
			return nil, err
		}
	}

	s.agentSessionRecoveryOnce.Do(func() {
		s.agentSessionRecoveryErr = store.RecoverInterruptedSessions(time.Now().UTC())
	})
	if s.agentSessionRecoveryErr != nil {
		return nil, fmt.Errorf("recover interrupted agent sessions: %w", s.agentSessionRecoveryErr)
	}
	return store, nil
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

	// The server owns session identity. It must exist before Run starts
	// so cancellation and progress can address the active operation.
	request.SessionID = uuid.NewString()

	store, err := s.resolveAgentSessionStore()
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	started := time.Now().UTC()

	snapshot := agentpkg.SessionSnapshot{
		ID:        request.SessionID,
		Model:     request.Model,
		Workspace: request.Workspace,
		Task:      request.Task,
		State:     agentpkg.SessionStateRunning,
		CreatedAt: started,
		UpdatedAt: started,
	}

	if err := store.Save(snapshot); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	if err := store.AppendEvent(
		request.SessionID,
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

	runner := s.agentRunner
	if runner == nil {
		runner = agentpkg.NewEngine(serverChatClient{server: s})
	}

	runContext, cancel := context.WithCancel(c.Request.Context())

	if !s.agentSessionCtl.register(request.SessionID, cancel) {
		cancel()
		c.JSON(
			http.StatusConflict,
			gin.H{"error": "session is already active"},
		)
		return
	}

	var lifecyclePersistedLive atomic.Bool
	request.OnLifecycleEvent = func(event agentpkg.SessionEvent) error {
		if err := s.persistLiveAgentLifecycleEvent(
			store,
			request.SessionID,
			event,
		); err != nil {
			return err
		}
		lifecyclePersistedLive.Store(true)
		return nil
	}

	result, runErr := runner.Run(runContext, request)

	// A runner cannot change the server-owned session identity.
	result.SessionID = request.SessionID

	cancelled, finishErr := s.finishAgentSession(
		store,
		request,
		result,
		runErr,
		lifecyclePersistedLive.Load(),
	)
	cancel()

	if finishErr != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": finishErr.Error()},
		)
		return
	}

	if cancelled {
		result.Status = agentpkg.StatusFailed
		result.FinalSummary = "session cancelled"
		c.JSON(http.StatusOK, result)
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

func (s *Server) AgentSessionContinueHandler(c *gin.Context) {
	store, err := s.resolveAgentSessionStore()
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	sessionID := c.Param("id")

	snapshot, err := store.Load(sessionID)
	if err != nil {
		c.JSON(
			http.StatusNotFound,
			gin.H{"error": err.Error()},
		)
		return
	}

	var request agentpkg.RunRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.AbortWithStatusJSON(
			http.StatusBadRequest,
			gin.H{"error": err.Error()},
		)
		return
	}

	if strings.TrimSpace(request.Task) == "" {
		c.AbortWithStatusJSON(
			http.StatusBadRequest,
			gin.H{"error": "task is required"},
		)
		return
	}

	request.SessionID = snapshot.ID
	request.Model = snapshot.Model
	request.Workspace = snapshot.Workspace

	runContext, cancel := context.WithCancel(c.Request.Context())

	ctl := &s.agentSessionCtl
	ctl.mu.Lock()

	if ctl.cancels == nil {
		ctl.cancels = make(map[string]context.CancelFunc)
	}

	if _, active := ctl.cancels[sessionID]; active {
		ctl.mu.Unlock()
		cancel()

		c.JSON(
			http.StatusConflict,
			gin.H{"error": "session is already active"},
		)
		return
	}

	snapshot.State = agentpkg.SessionStateRunning
	snapshot.Task = request.Task
	snapshot.UpdatedAt = time.Now().UTC()
	snapshot.StepsExecuted = 0
	snapshot.ChangedFiles = nil
	snapshot.VerificationResults = nil
	snapshot.FinalSummary = ""

	if err := store.Save(snapshot); err != nil {
		ctl.mu.Unlock()
		cancel()

		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	if err := store.AppendEvent(
		sessionID,
		agentpkg.SessionEvent{
			Type:      agentpkg.EventSessionContinued,
			Timestamp: snapshot.UpdatedAt,
			Message:   "session continued",
		},
	); err != nil {
		ctl.mu.Unlock()
		cancel()

		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	ctl.cancels[sessionID] = cancel
	ctl.mu.Unlock()

	var lifecyclePersistedLive atomic.Bool
	request.OnLifecycleEvent = func(event agentpkg.SessionEvent) error {
		if err := s.persistLiveAgentLifecycleEvent(
			store,
			sessionID,
			event,
		); err != nil {
			return err
		}
		lifecyclePersistedLive.Store(true)
		return nil
	}

	runner := s.agentRunner
	if runner == nil {
		runner = agentpkg.NewEngine(serverChatClient{server: s})
	}

	result, runErr := runner.Run(runContext, request)

	result.SessionID = sessionID

	cancelled, finishErr := s.finishAgentSession(
		store,
		request,
		result,
		runErr,
		lifecyclePersistedLive.Load(),
	)
	cancel()

	if finishErr != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": finishErr.Error()},
		)
		return
	}

	if cancelled {
		result.Status = agentpkg.StatusFailed
		result.FinalSummary = "session cancelled"
		c.JSON(http.StatusOK, result)
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

func (s *Server) AgentSessionCancelHandler(c *gin.Context) {
	store, err := s.resolveAgentSessionStore()
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	sessionID := c.Param("id")
	ctl := &s.agentSessionCtl

	ctl.mu.Lock()

	cancel, active := ctl.cancels[sessionID]
	if !active {
		snapshot, loadErr := store.Load(sessionID)
		ctl.mu.Unlock()

		if loadErr != nil {
			c.JSON(
				http.StatusNotFound,
				gin.H{"error": loadErr.Error()},
			)
			return
		}

		if snapshot.State == agentpkg.SessionStateCancelled {
			c.JSON(http.StatusOK, snapshot)
			return
		}

		c.JSON(
			http.StatusConflict,
			gin.H{"error": "session is not active"},
		)
		return
	}

	snapshot, err := store.Load(sessionID)
	if err != nil {
		ctl.mu.Unlock()

		c.JSON(
			http.StatusNotFound,
			gin.H{"error": err.Error()},
		)
		return
	}

	now := time.Now().UTC()

	if err := store.AppendEvent(
		sessionID,
		agentpkg.SessionEvent{
			Type:      agentpkg.EventCancelRequested,
			Timestamp: now,
			Message:   "cancellation requested",
		},
	); err != nil {
		ctl.mu.Unlock()

		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	snapshot.State = agentpkg.SessionStateCancelled
	snapshot.UpdatedAt = now
	snapshot.FinalSummary = "session cancelled"

	if err := store.Save(snapshot); err != nil {
		ctl.mu.Unlock()

		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	if err := store.AppendEvent(
		sessionID,
		agentpkg.SessionEvent{
			Type:      agentpkg.EventCancelled,
			Timestamp: now,
			Message:   "session cancelled",
		},
	); err != nil {
		ctl.mu.Unlock()

		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": err.Error()},
		)
		return
	}

	// Persist CANCELLED before signalling the execution context.
	// finishAgentSession uses this same mutex, so a late success
	// can never race past the persisted cancellation.
	cancel()

	ctl.mu.Unlock()

	c.JSON(http.StatusOK, snapshot)
}

func (s *Server) finishAgentSession(
	store sessionStore,
	request agentpkg.RunRequest,
	result agentpkg.RunResult,
	runErr error,
	lifecyclePersistedLive bool,
) (bool, error) {
	ctl := &s.agentSessionCtl

	ctl.mu.Lock()
	defer ctl.mu.Unlock()

	if ctl.cancels != nil {
		delete(ctl.cancels, request.SessionID)
	}

	snapshot, err := store.Load(request.SessionID)
	if err != nil {
		return false, fmt.Errorf("load session before finalization: %w", err)
	}

	// Cancellation is terminal and wins over any late runner response.
	if snapshot.State == agentpkg.SessionStateCancelled {
		return true, nil
	}

	now := time.Now().UTC()

	snapshot.Model = request.Model
	snapshot.Workspace = request.Workspace
	snapshot.Task = request.Task
	snapshot.UpdatedAt = now
	snapshot.StepsExecuted = result.StepsExecuted
	snapshot.ChangedFiles = result.ChangedFiles
	snapshot.VerificationResults = result.VerificationResults
	snapshot.FinalSummary = result.FinalSummary

	eventType := agentpkg.EventFailed
	eventMessage := "session failed"
	snapshot.State = agentpkg.SessionStateFailed

	if runErr == nil && result.Status == agentpkg.StatusSuccess {
		snapshot.State = agentpkg.SessionStateVerified
		eventType = agentpkg.EventCompleted
		eventMessage = "session completed"
	}

	if strings.TrimSpace(result.GitDiff) != "" {
		if err := store.SaveDiff(
			request.SessionID,
			result.GitDiff,
		); err != nil {
			return false, fmt.Errorf("save final diff: %w", err)
		}
	}

	if err := store.Save(snapshot); err != nil {
		return false, fmt.Errorf("save final session: %w", err)
	}

	if !lifecyclePersistedLive {
		for _, event := range result.LifecycleEvents {
			if err := store.AppendEvent(
				request.SessionID,
				event,
			); err != nil {
				return false, fmt.Errorf(
					"append lifecycle event %q: %w",
					event.Type,
					err,
				)
			}
		}
	}

	if result.ContextBuilt {
		if err := store.AppendEvent(
			request.SessionID,
			agentpkg.SessionEvent{
				Type:      agentpkg.EventContextBuilt,
				Timestamp: now,
				Message:   "repository context built",
			},
		); err != nil {
			return false, fmt.Errorf(
				"append context-built event: %w",
				err,
			)
		}
	}
	if err := store.AppendEvent(
		request.SessionID,
		agentpkg.SessionEvent{
			Type:      eventType,
			Timestamp: now,
			Message:   eventMessage,
		},
	); err != nil {
		return false, fmt.Errorf("append final event: %w", err)
	}

	return false, nil
}

func (s *Server) persistLiveAgentLifecycleEvent(
	store sessionStore,
	sessionID string,
	event agentpkg.SessionEvent,
) error {
	ctl := &s.agentSessionCtl

	ctl.mu.Lock()
	defer ctl.mu.Unlock()

	snapshot, err := store.Load(sessionID)
	if err != nil {
		return fmt.Errorf("load session for lifecycle event: %w", err)
	}

	// Cancellation is terminal. A callback that arrives after cancellation
	// must not append an event or overwrite the persisted state.
	if snapshot.State == agentpkg.SessionStateCancelled {
		return nil
	}

	snapshot.State = liveAgentLifecycleState(snapshot.State, event.Type)
	snapshot.UpdatedAt = event.Timestamp
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = time.Now().UTC()
	}

	if err := store.Save(snapshot); err != nil {
		return fmt.Errorf("save session for lifecycle event %q: %w", event.Type, err)
	}

	if err := store.AppendEvent(sessionID, event); err != nil {
		return fmt.Errorf("append live lifecycle event %q: %w", event.Type, err)
	}

	return nil
}

func liveAgentLifecycleState(
	current agentpkg.SessionState,
	eventType agentpkg.SessionEventType,
) agentpkg.SessionState {
	switch eventType {
	case agentpkg.EventEditingStarted:
		if current == agentpkg.SessionStateRunning {
			return agentpkg.SessionStateEditing
		}
		return current
	case agentpkg.EventDiffDetected:
		if current == agentpkg.SessionStateRunning ||
			current == agentpkg.SessionStateEditing {
			return agentpkg.SessionStateDiffDetected
		}
		return current
	case agentpkg.EventVerificationStart:
		if current == agentpkg.SessionStateVerifyFailed ||
			current == agentpkg.SessionStateRepairing ||
			current == agentpkg.SessionStateReverifying {
			return agentpkg.SessionStateReverifying
		}
		return agentpkg.SessionStateVerifying
	case agentpkg.EventVerificationFailed:
		return agentpkg.SessionStateVerifyFailed
	case agentpkg.EventRepairStarted:
		return agentpkg.SessionStateRepairing
	case agentpkg.EventRepairCompleted:
		return agentpkg.SessionStateReverifying
	case agentpkg.EventVerificationPassed:
		return agentpkg.SessionStateVerified
	default:
		return current
	}
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
