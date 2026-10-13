package muse

import "time"

type EventType string

const (
	EventGoalCreated       EventType = "goal_created"
	EventPlanCreated       EventType = "plan_created"
	EventStepStarted       EventType = "step_started"
	EventRiskClassified    EventType = "risk_classified"
	EventApprovalRequested EventType = "approval_requested"
	EventApprovalAllowed   EventType = "approval_allowed"
	EventApprovalDenied    EventType = "approval_denied"
	EventToolStarted       EventType = "tool_started"
	EventToolCompleted     EventType = "tool_completed"
	EventToolFailed        EventType = "tool_failed"
	EventVerificationStart EventType = "verification_started"
	EventVerificationPass  EventType = "verification_passed"
	EventVerificationFail  EventType = "verification_failed"
	EventRepairStart       EventType = "repair_started"
	EventCheckpoint        EventType = "checkpoint_created"
	EventRollback          EventType = "rolled_back"
	EventRemembered        EventType = "remembered"
	EventCompleted         EventType = "completed"
	EventFailed            EventType = "failed"
)

type Event struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id,omitempty"`
	Type      EventType      `json:"type"`
	Action    Action         `json:"action,omitempty"`
	Risk      Risk           `json:"risk,omitempty"`
	Target    string         `json:"target,omitempty"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

func NewEvent(sessionID string, kind EventType, action Action, target, message string) Event {
	return Event{
		ID:        newID("event"),
		SessionID: sessionID,
		Type:      kind,
		Action:    action,
		Risk:      RiskFor(action),
		Target:    target,
		Message:   message,
		Timestamp: time.Now().UTC(),
	}
}
