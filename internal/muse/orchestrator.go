package muse

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Orchestrator struct {
	Runtime  *Runtime
	Planner  Planner
	Verifier *Verifier

	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewOrchestrator(runtime *Runtime, planner Planner) *Orchestrator {
	if planner == nil {
		planner = StaticPlanner{}
	}
	return &Orchestrator{
		Runtime: runtime, Planner: planner,
		sessions: map[string]*Session{},
	}
}

func (o *Orchestrator) CreateGoal(ctx context.Context, goal string) (*Session, error) {
	if o == nil || o.Runtime == nil {
		return nil, errors.New("Muse runtime unavailable")
	}
	id := newID("session")
	s := &Session{ID: id, Goal: goal, State: SessionPlanned, StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	plan, err := o.Planner.Plan(ctx, id, goal)
	if err != nil {
		s.State = SessionFailed
		s.LastError = err.Error()
		return s, err
	}
	s.Plan = &plan
	o.mu.Lock()
	o.sessions[id] = s
	o.mu.Unlock()
	o.Runtime.emit(NewEvent(id, EventGoalCreated, ActionUnknown, "", goal))
	o.Runtime.emit(NewEvent(id, EventPlanCreated, ActionUnknown, "", plan.ID))
	return s, nil
}

func (o *Orchestrator) Session(id string) (*Session, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	s, ok := o.sessions[id]
	if !ok {
		return nil, false
	}
	cp := *s
	return &cp, true
}

func (o *Orchestrator) Run(ctx context.Context, id string) (*Session, error) {
	o.mu.Lock()
	s, ok := o.sessions[id]
	if !ok {
		o.mu.Unlock()
		return nil, errors.New("session not found")
	}
	if s.Plan == nil {
		o.mu.Unlock()
		return nil, errors.New("session has no plan")
	}
	s.State = SessionExecuting
	s.UpdatedAt = time.Now().UTC()
	o.mu.Unlock()

	for i, step := range s.Plan.Steps {
		o.mu.Lock()
		s.CurrentStep = i
		s.UpdatedAt = time.Now().UTC()
		o.mu.Unlock()

		req := Request{
			ID: newID("request"), SessionID: s.ID, Action: step.Action,
			Target: step.Target, Args: step.Args, CreatedAt: time.Now().UTC(),
		}
		result := o.Runtime.Execute(ctx, req)
		if !result.OK {
			o.mu.Lock()
			s.State = SessionFailed
			s.LastError = result.Error
			s.UpdatedAt = time.Now().UTC()
			o.mu.Unlock()
			return s, errors.New(result.Error)
		}
		if len(step.Verify) > 0 && o.Verifier != nil {
			o.mu.Lock()
			s.State = SessionVerifying
			o.mu.Unlock()
			o.Runtime.emit(NewEvent(s.ID, EventVerificationStart, step.Action, step.Target, "verification"))
			if _, err := o.Verifier.Run(ctx, step.Verify); err != nil {
				o.Runtime.emit(NewEvent(s.ID, EventVerificationFail, step.Action, step.Target, err.Error()))
				o.mu.Lock()
				s.State = SessionFailed
				s.LastError = err.Error()
				s.UpdatedAt = time.Now().UTC()
				o.mu.Unlock()
				return s, err
			}
			o.Runtime.emit(NewEvent(s.ID, EventVerificationPass, step.Action, step.Target, "passed"))
		}
	}

	o.mu.Lock()
	s.State = SessionCompleted
	s.CompletedAt = time.Now().UTC()
	s.UpdatedAt = s.CompletedAt
	o.mu.Unlock()
	o.Runtime.emit(NewEvent(s.ID, EventCompleted, ActionUnknown, "", "goal completed"))
	return s, nil
}
