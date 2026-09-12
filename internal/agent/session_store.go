package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sessionFileName = "session.json"
	eventsFileName  = "events.jsonl"
	diffFileName    = "latest.diff"
)

type SessionState string

const (
	SessionStateRunning SessionState = "RUNNING"
)

type SessionEventType string

const (
	EventSessionCreated SessionEventType = "session_created"
	EventContextBuilt   SessionEventType = "context_built"
)

type SessionSnapshot struct {
	ID                  string               `json:"id"`
	Model               string               `json:"model"`
	Workspace           string               `json:"workspace"`
	Task                string               `json:"task"`
	State               SessionState         `json:"state"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
	StepsExecuted       int                  `json:"steps_executed,omitempty"`
	ChangedFiles        []string             `json:"changed_files,omitempty"`
	VerificationResults []VerificationResult `json:"verification_results,omitempty"`
	FinalSummary        string               `json:"final_summary,omitempty"`
}

type SessionEvent struct {
	Type      SessionEventType `json:"type"`
	Timestamp time.Time        `json:"timestamp"`
	Message   string           `json:"message,omitempty"`
}

type SessionStore struct {
	root string
}

func NewSessionStore(root string) (*SessionStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("session store root is required")
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve session store root: %w", err)
	}

	if err := os.MkdirAll(absoluteRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create session store root: %w", err)
	}

	return &SessionStore{root: absoluteRoot}, nil
}

func (s *SessionStore) Save(snapshot SessionSnapshot) error {
	if s == nil {
		return errors.New("session store is nil")
	}
	if err := validateSessionID(snapshot.ID); err != nil {
		return err
	}

	sessionDir := s.sessionDir(snapshot.ID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	data = append(data, '\n')

	target := filepath.Join(sessionDir, sessionFileName)
	temp := target + ".tmp"

	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return fmt.Errorf("write temporary session: %w", err)
	}

	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("replace session snapshot: %w", err)
	}

	return nil
}

func (s *SessionStore) Load(sessionID string) (SessionSnapshot, error) {
	var snapshot SessionSnapshot

	if s == nil {
		return snapshot, errors.New("session store is nil")
	}
	if err := validateSessionID(sessionID); err != nil {
		return snapshot, err
	}

	data, err := os.ReadFile(filepath.Join(s.sessionDir(sessionID), sessionFileName))
	if err != nil {
		return snapshot, fmt.Errorf("read session: %w", err)
	}

	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, fmt.Errorf("decode session: %w", err)
	}

	if snapshot.ID != sessionID {
		return snapshot, fmt.Errorf(
			"session id mismatch: file contains %q, requested %q",
			snapshot.ID,
			sessionID,
		)
	}

	return snapshot, nil
}

func (s *SessionStore) AppendEvent(sessionID string, event SessionEvent) error {
	if s == nil {
		return errors.New("session store is nil")
	}
	if err := s.ensureSessionExists(sessionID); err != nil {
		return err
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode session event: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(s.sessionDir(sessionID), eventsFileName)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("append session event: %w", err)
	}

	return nil
}

func (s *SessionStore) Events(sessionID string) ([]SessionEvent, error) {
	if s == nil {
		return nil, errors.New("session store is nil")
	}
	if err := s.ensureSessionExists(sessionID); err != nil {
		return nil, err
	}

	path := filepath.Join(s.sessionDir(sessionID), eventsFileName)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []SessionEvent{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	events := make([]SessionEvent, 0)

	for {
		var event SessionEvent

		err := decoder.Decode(&event)
		if errors.Is(err, os.ErrClosed) {
			return nil, err
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return events, nil
			}
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode session event: %w", err)
		}

		events = append(events, event)
	}

	return events, nil
}

func (s *SessionStore) SaveDiff(sessionID, diff string) error {
	if s == nil {
		return errors.New("session store is nil")
	}
	if err := s.ensureSessionExists(sessionID); err != nil {
		return err
	}

	target := filepath.Join(s.sessionDir(sessionID), diffFileName)
	temp := target + ".tmp"

	if err := os.WriteFile(temp, []byte(diff), 0o600); err != nil {
		return fmt.Errorf("write temporary diff: %w", err)
	}

	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("replace diff: %w", err)
	}

	return nil
}

func (s *SessionStore) LoadDiff(sessionID string) (string, error) {
	if s == nil {
		return "", errors.New("session store is nil")
	}
	if err := s.ensureSessionExists(sessionID); err != nil {
		return "", err
	}

	data, err := os.ReadFile(filepath.Join(s.sessionDir(sessionID), diffFileName))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read diff: %w", err)
	}

	return string(data), nil
}

func (s *SessionStore) sessionDir(sessionID string) string {
	return filepath.Join(s.root, sessionID)
}

func (s *SessionStore) ensureSessionExists(sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}

	path := filepath.Join(s.sessionDir(sessionID), sessionFileName)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("session %q does not exist: %w", sessionID, err)
	}

	return nil
}

func validateSessionID(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)

	if sessionID == "" {
		return errors.New("session id is required")
	}

	if sessionID == "." ||
		sessionID == ".." ||
		strings.Contains(sessionID, "/") ||
		strings.Contains(sessionID, `\`) {
		return fmt.Errorf("invalid session id %q", sessionID)
	}

	return nil
}
