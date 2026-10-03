package agent

import (
	"strconv"
	"strings"
	"time"

	"github.com/ollama/ollama/envconfig"
)

// OwnBot is the operator's own agent, so every engine budget is operator
// configurable and the defaults never refuse or truncate requested work.
// Unset or 0 means "no limit" for every cap below; set a variable to a
// positive value to re-impose a bound.
const (
	// agentMaxTurnsEnv caps model turns per run. Unset/0 runs until the task
	// is finished, cancelled, or the stagnation guard stops the loop.
	agentMaxTurnsEnv = "OLLAMA_AGENT_MAX_TURNS"
	// agentToolCallsPerTurnEnv caps tool calls executed from a single model
	// response. Unset/0 executes every call the model requests.
	agentToolCallsPerTurnEnv = "OLLAMA_AGENT_TOOL_CALLS_PER_TURN"
	// agentStagnantTurnsEnv caps consecutive turns without new observations
	// or meaningful source changes. Defaults to 4; 0 disables the guard.
	agentStagnantTurnsEnv = "OLLAMA_AGENT_STAGNANT_TURNS"
	// agentContextWindowEnv is a legacy optional upper bound for agent context.
	agentContextWindowEnv = "OLLAMA_AGENT_CONTEXT_WINDOW"
	agentMaxContextEnv    = "OWNBOT_MAX_CONTEXT_TOKENS"
	agentMinContextEnv    = "OWNBOT_MIN_CONTEXT_TOKENS"
	agentRAMReserveEnv    = "OWNBOT_RAM_RESERVE_GB"
	// agentConversationMessagesEnv caps messages replayed to the model.
	// Unset/0 replays the full conversation.
	agentConversationMessagesEnv = "OLLAMA_AGENT_CONVERSATION_MESSAGES"
	// agentObservationBytesEnv caps a single tool observation passed back to
	// the model. Unset/0 passes the full observation.
	agentObservationBytesEnv = "OLLAMA_AGENT_OBSERVATION_BYTES"
	// agentCommandTimeoutEnv is the number of seconds a shell command may run
	// before it is killed. Unset/0 runs until the command finishes or the
	// session is cancelled.
	agentCommandTimeoutEnv = "OLLAMA_AGENT_COMMAND_TIMEOUT"
	// agentOutputLimitEnv caps captured stdout/stderr bytes per command.
	// Unset/0 captures everything the command writes.
	agentOutputLimitEnv = "OLLAMA_AGENT_OUTPUT_LIMIT"
)

const (
	defaultAgentMaxContextTokens = 32768
	defaultAgentMinContextTokens = 4096
	defaultAgentRAMReserveGB     = 4
)

// defaultAgentStagnantTurns stops an agent that is clearly looping.
const defaultAgentStagnantTurns = 4

func agentPositiveEnv(name string, fallback int) int {
	raw := strings.TrimSpace(envconfig.Var(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func agentTurnLimit() int { return agentPositiveEnv(agentMaxTurnsEnv, 0) }

func agentToolCallsPerTurnLimit() int {
	return agentPositiveEnv(agentToolCallsPerTurnEnv, 0)
}

func agentStagnationLimit() int {
	return agentPositiveEnv(agentStagnantTurnsEnv, defaultAgentStagnantTurns)
}

func agentContextWindowLimit() int {
	return agentPositiveEnv(agentContextWindowEnv, 0)
}

func agentMaxContextTokens() int {
	return agentPositiveEnv(agentMaxContextEnv, defaultAgentMaxContextTokens)
}

func agentMinContextTokens() int {
	return agentPositiveEnv(agentMinContextEnv, defaultAgentMinContextTokens)
}

func agentRAMReserveGB() int {
	return agentPositiveEnv(agentRAMReserveEnv, defaultAgentRAMReserveGB)
}

func agentConversationLimit() int {
	return agentPositiveEnv(agentConversationMessagesEnv, 0)
}

func agentObservationLimit() int {
	return agentPositiveEnv(agentObservationBytesEnv, 0)
}

// agentCommandTimeout returns the shell-command timeout. A negative duration
// tells the tools package to run without a timeout.
func agentCommandTimeout() time.Duration {
	raw := strings.TrimSpace(envconfig.Var(agentCommandTimeoutEnv))
	if raw == "" {
		return -1
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return -1
	}
	return time.Duration(seconds) * time.Second
}

// agentOutputLimit returns the per-command capture limit. A negative limit
// tells the tools package to capture all output.
func agentOutputLimit() int {
	raw := strings.TrimSpace(envconfig.Var(agentOutputLimitEnv))
	if raw == "" {
		return -1
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return -1
	}
	return value
}
