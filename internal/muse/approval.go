package muse

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Decision string

const (
	DecisionPending Decision = "pending"
	DecisionAllow   Decision = "allow"
	DecisionDeny    Decision = "deny"
)

type Approval struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id,omitempty"`
	Action    Action    `json:"action"`
	Risk      Risk      `json:"risk"`
	Target    string    `json:"target,omitempty"`
	Summary   string    `json:"summary"`
	Decision  Decision  `json:"decision"`
	CreatedAt time.Time `json:"created_at"`
	DecidedAt time.Time `json:"decided_at,omitempty"`
}

var (
	ErrApprovalRequired = errors.New("owner approval required")
	ErrApprovalDenied   = errors.New("owner approval denied")
)

type pendingApproval struct {
	item Approval
	ch   chan Decision
}

type ApprovalStore struct {
	mu      sync.RWMutex
	pending map[string]*pendingApproval
	history []Approval
}

func NewApprovalStore() *ApprovalStore {
	return &ApprovalStore{pending: map[string]*pendingApproval{}}
}

func (s *ApprovalStore) Request(req Request) Approval {
	item := Approval{
		ID:        newID("approval"),
		SessionID: req.SessionID,
		Action:    req.Action,
		Risk:      RiskFor(req.Action),
		Target:    req.Target,
		Summary:   string(req.Action) + " " + req.Target,
		Decision:  DecisionPending,
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.pending[item.ID] = &pendingApproval{item: item, ch: make(chan Decision, 1)}
	s.mu.Unlock()
	return item
}

func (s *ApprovalStore) Pending() []Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Approval, 0, len(s.pending))
	for _, p := range s.pending {
		out = append(out, p.item)
	}
	return out
}

func (s *ApprovalStore) History() []Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Approval, len(s.history))
	copy(out, s.history)
	return out
}

func (s *ApprovalStore) Decide(id string, decision Decision) error {
	if decision != DecisionAllow && decision != DecisionDeny {
		return fmt.Errorf("invalid approval decision %q", decision)
	}
	s.mu.Lock()
	p, ok := s.pending[id]
	if !ok {
		s.mu.Unlock()
		return errors.New("approval request not found")
	}
	delete(s.pending, id)
	p.item.Decision = decision
	p.item.DecidedAt = time.Now().UTC()
	s.history = append(s.history, p.item)
	s.mu.Unlock()
	select {
	case p.ch <- decision:
	default:
	}
	return nil
}

func (s *ApprovalStore) Wait(ctx context.Context, item Approval, policy Policy) (Decision, error) {
	if !policy.RequireApproval {
		return DecisionAllow, nil
	}
	s.mu.RLock()
	p, ok := s.pending[item.ID]
	s.mu.RUnlock()
	if !ok {
		return DecisionDeny, errors.New("approval request is no longer pending")
	}
	// Remove abandoned approvals when waiting ends.
	defer func() {
		s.mu.Lock()
		if s.pending[item.ID] == p {
			delete(s.pending, item.ID)
		}
		s.mu.Unlock()
	}()

	if policy.TimeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(policy.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		select {
		case d := <-p.ch:
			return d, nil
		case <-timer.C:
			s.mu.Lock()
			delete(s.pending, item.ID)
			s.mu.Unlock()
			if policy.OnTimeout == TimeoutAllow {
				return DecisionAllow, nil
			}
			return DecisionDeny, ErrApprovalDenied
		case <-ctx.Done():
			return DecisionDeny, ctx.Err()
		}
	}

	select {
	case d := <-p.ch:
		return d, nil
	case <-ctx.Done():
		return DecisionDeny, ctx.Err()
	}
}
