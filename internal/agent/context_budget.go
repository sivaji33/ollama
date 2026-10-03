package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/discover"
)

const (
	agentOutputTokenReserve = 2048
	agentPromptEstimatePad  = 256
	bytesPerEstimatedToken  = 3
)

type ContextStatus struct {
	TotalRAMGB            float64 `json:"total_ram_gb"`
	AvailableRAMGB        float64 `json:"available_ram_gb"`
	RAMReserveGB          float64 `json:"ram_reserve_gb"`
	SelectedContextTokens int     `json:"selected_context_tokens"`
	MaxContextTokens      int     `json:"max_context_tokens"`
	EstimatedPromptTokens int     `json:"estimated_prompt_tokens"`
	ReservedOutputTokens  int     `json:"reserved_output_tokens"`
	ContextSafe           bool    `json:"context_safe"`
}

func CurrentContextStatus() (ContextStatus, error) {
	memory, err := discover.GetCPUMem()
	if err != nil {
		return ContextStatus{}, fmt.Errorf("detect system memory: %w", err)
	}
	return contextStatus(memory.TotalMemory, memory.FreeMemory), nil
}

func contextStatus(totalBytes, availableBytes uint64) ContextStatus {
	reserveGB := agentRAMReserveGB()
	totalGB := float64(totalBytes) / float64(1<<30)
	availableGB := float64(availableBytes) / float64(1<<30)
	maxTokens := agentMaxContextTokens()
	minTokens := agentMinContextTokens()
	if legacyLimit := agentContextWindowLimit(); legacyLimit > 0 && legacyLimit < maxTokens {
		maxTokens = legacyLimit
	}
	if maxTokens > defaultAgentMaxContextTokens {
		maxTokens = defaultAgentMaxContextTokens
	}
	if maxTokens < 1 {
		maxTokens = defaultAgentMaxContextTokens
	}
	if minTokens > maxTokens {
		minTokens = maxTokens
	}
	if minTokens < 1 {
		minTokens = defaultAgentMinContextTokens
	}

	usableGB := math.Max(0, availableGB-float64(reserveGB))
	selected := contextForUsableRAM(usableGB)
	if selected < minTokens {
		selected = minTokens
	}
	if selected > maxTokens {
		selected = maxTokens
	}

	return ContextStatus{
		TotalRAMGB:            roundRAMGB(totalGB),
		AvailableRAMGB:        roundRAMGB(availableGB),
		RAMReserveGB:          float64(reserveGB),
		SelectedContextTokens: selected,
		MaxContextTokens:      maxTokens,
		EstimatedPromptTokens: 0,
		ReservedOutputTokens:  outputTokenReserve(selected),
		ContextSafe:           selected > outputTokenReserve(selected),
	}
}

func roundRAMGB(value float64) float64 {
	return math.Round(value*10) / 10
}

func contextForUsableRAM(usableGB float64) int {
	switch {
	case usableGB < 4:
		return 4096
	case usableGB < 8:
		return 8192
	case usableGB < 16:
		return 16384
	default:
		return 32768
	}
}

func estimatePromptTokens(messages []api.Message, tools api.Tools) int {
	payload, err := json.Marshal(struct {
		Messages []api.Message `json:"messages"`
		Tools    api.Tools     `json:"tools"`
	}{Messages: messages, Tools: tools})
	if err != nil {
		slog.Error("estimate agent prompt size", "error", err)
		return math.MaxInt
	}
	return (len(payload)+bytesPerEstimatedToken-1)/bytesPerEstimatedToken + agentPromptEstimatePad
}

func prepareAgentPrompt(messages []api.Message, tools api.Tools, contextLimit int) ([]api.Message, int, error) {
	prepared := removeDuplicateSystemMessages(messages)
	initialTokens := estimatePromptTokens(prepared, tools)
	reserveTokens := outputTokenReserve(contextLimit)

	if initialTokens > promptBudget(contextLimit, reserveTokens) {
		compacted := compactRepositoryContext(prepared)
		if !messagesEqual(prepared, compacted) {
			prepared = compacted
		}
	}
	for estimatePromptTokens(prepared, tools) > promptBudget(contextLimit, reserveTokens) {
		turns := agentHistoryTurns(prepared)
		if len(turns) == 0 {
			break
		}
		prepared = append([]api.Message(nil), prepared[:2]...)
		for _, turn := range turns[1:] {
			prepared = append(prepared, turn...)
		}
	}

	finalTokens := estimatePromptTokens(prepared, tools)
	if finalTokens != initialTokens {
		slog.Info("[context] prompt reduced",
			"prompt_tokens", initialTokens,
			"context_limit", contextLimit,
			"action", "compact_history",
			"final_prompt_tokens", finalTokens)
	}
	if finalTokens > promptBudget(contextLimit, reserveTokens) {
		return nil, finalTokens, fmt.Errorf("agent prompt cannot fit safely: estimated %d prompt tokens plus %d reserved output tokens exceeds context limit %d", finalTokens, reserveTokens, contextLimit)
	}
	return prepared, finalTokens, nil
}

func promptBudget(contextLimit, reserveTokens int) int {
	return contextLimit - reserveTokens
}

func outputTokenReserve(contextLimit int) int {
	if contextLimit <= 4096 {
		return 1024
	}
	return agentOutputTokenReserve
}

func removeDuplicateSystemMessages(messages []api.Message) []api.Message {
	result := make([]api.Message, 0, len(messages))
	keptSystem := false
	for _, message := range messages {
		if message.Role == "system" {
			if keptSystem {
				continue
			}
			keptSystem = true
		}
		result = append(result, message)
	}
	return result
}

func compactRepositoryContext(messages []api.Message) []api.Message {
	result := append([]api.Message(nil), messages...)
	for i := range result {
		if result[i].Role != "user" {
			continue
		}
		const marker = "\n\nRepository context:\n"
		if before, _, ok := strings.Cut(result[i].Content, marker); ok {
			result[i].Content = before + "\n\nRepository context omitted to fit the prompt budget; inspect the workspace with tools."
		}
	}
	return result
}

func agentHistoryTurns(messages []api.Message) [][]api.Message {
	var turns [][]api.Message
	for i := 2; i < len(messages); {
		start := i
		message := messages[i]
		i++
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			for _, call := range message.ToolCalls {
				if i >= len(messages) || messages[i].Role != "tool" || messages[i].ToolCallID != call.ID {
					break
				}
				i++
			}
		} else if message.Role == "tool" {
			continue
		}
		turns = append(turns, messages[start:i])
	}
	return turns
}

func messagesEqual(left, right []api.Message) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func lowerContextCandidates(selected, minimum int) []int {
	candidates := []int{32768, 16384, 8192, 4096}
	var lower []int
	for _, candidate := range candidates {
		if candidate < selected && candidate >= minimum {
			lower = append(lower, candidate)
		}
	}
	if minimum < selected && (len(lower) == 0 || lower[len(lower)-1] != minimum) {
		lower = append(lower, minimum)
	}
	return lower
}

func isContextSizeError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "exceed_context_size_error") ||
		strings.Contains(message, "exceeds the available context size") ||
		strings.Contains(message, "input length exceeds the context length") ||
		(strings.Contains(message, "context") && strings.Contains(message, "exceed"))
}

func logContextStatus(status ContextStatus, estimatedPrompt int) {
	slog.Info(fmt.Sprintf(
		"[context] total_ram_gb=%.1f available_ram_gb=%.1f reserve_ram_gb=%.1f selected_context=%d estimated_prompt_tokens=%d reserved_output_tokens=%d final_budget=%d context_safe=%t",
		status.TotalRAMGB,
		status.AvailableRAMGB,
		status.RAMReserveGB,
		status.SelectedContextTokens,
		estimatedPrompt,
		outputTokenReserve(status.SelectedContextTokens),
		estimatedPrompt+outputTokenReserve(status.SelectedContextTokens),
		estimatedPrompt+outputTokenReserve(status.SelectedContextTokens) <= status.SelectedContextTokens,
	))
}
