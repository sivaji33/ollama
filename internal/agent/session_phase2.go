package agent

const DefaultAgentModel = "qwen3:4b-instruct"

const (
	SessionStateEditing      SessionState = "EDITING"
	SessionStateDiffDetected SessionState = "DIFF_DETECTED"
	SessionStateVerifying    SessionState = "VERIFYING"
	SessionStateVerifyFailed SessionState = "VERIFY_FAILED"
	SessionStateRepairing    SessionState = "REPAIRING"
	SessionStateReverifying  SessionState = "REVERIFYING"
	SessionStateVerified     SessionState = "VERIFIED"
	SessionStateFailed       SessionState = "FAILED"
	SessionStateCancelled    SessionState = "CANCELLED"
)

const (
	EventModelTurn          SessionEventType = "model_turn"
	EventToolCall           SessionEventType = "tool_call"
	EventToolResult         SessionEventType = "tool_result"
	EventEditDetected       SessionEventType = "edit_detected"
	EventVerificationStart  SessionEventType = "verification_started"
	EventVerificationPassed SessionEventType = "verification_passed"
	EventVerificationFailed SessionEventType = "verification_failed"
	EventRepairStarted      SessionEventType = "repair_started"
	EventRepairCompleted    SessionEventType = "repair_completed"
	EventCancelRequested    SessionEventType = "cancel_requested"
	EventCancelled          SessionEventType = "cancelled"
	EventCompleted          SessionEventType = "completed"
	EventFailed             SessionEventType = "failed"
)

func (s *SessionStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}
