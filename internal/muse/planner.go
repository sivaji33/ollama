package muse

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Planner interface {
	Plan(context.Context, string, string) (Plan, error)
}

type StaticPlanner struct{}

func (StaticPlanner) Plan(_ context.Context, sessionID, goal string) (Plan, error) {
	if strings.TrimSpace(goal) == "" {
		return Plan{}, errors.New("goal is required")
	}
	return Plan{
		ID:        newID("plan"),
		SessionID: sessionID,
		Goal:      goal,
		Steps: []PlanStep{{
			ID:          newID("step"),
			Description: "Inspect the workspace before taking any consequential action.",
			Action:      ActionListFiles,
			Target:      ".",
		}},
		Acceptance: []string{"goal reviewed", "tool evidence collected"},
		CreatedAt:  time.Now().UTC(),
	}, nil
}
