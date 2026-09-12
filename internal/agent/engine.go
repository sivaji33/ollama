package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ollama/ollama/api"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

type Engine struct{ chat ChatClient }

const systemPrompt = `This is a modification task. Use the native provided tools to inspect and edit the workspace. Assistant prose does not perform filesystem changes, and JSON printed as assistant content is NOT a tool call. Follow the native tool-call syntax required by your model template. Do not claim completion before a tool-produced meaningful source edit exists. After editing, inspect git_diff. Completion requires successful verification.`

const toolRetryPrompt = `Your previous response did not execute a tool. This task requires an actual repository change. Use one of the provided native tools now. Do not print JSON or describe a tool call in assistant text. Follow the tool-call format provided by your model template.`

func NewEngine(chat ChatClient) *Engine { return &Engine{chat: chat} }

func (e *Engine) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	result := RunResult{SessionID: uuid.NewString(), Status: StatusFailed, ToolCalls: []ToolCallRecord{}, ChangedFiles: []string{}, VerificationResults: []VerificationResult{}, StepEvents: []StepEvent{}}
	if e == nil || e.chat == nil {
		return result, errors.New("agent requires a chat client")
	}
	if strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Task) == "" {
		return result, errors.New("model and task are required")
	}
	workspace, err := agenttools.NewWorkspace(request.Workspace)
	if err != nil {
		return result, err
	}
	maxSteps := request.MaxSteps
	if maxSteps == 0 {
		maxSteps = DefaultMaxSteps
	}
	if maxSteps < 1 || maxSteps > 100 {
		return result, errors.New("max_steps must be between 1 and 100")
	}
	inspection, err := workspace.SearchFiles("", 100)
	if err != nil {
		return result, err
	}
	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: request.Task + "\n\nInitial workspace file listing:\n" + inspection},
	}
	baselineDiff, _, err := workspace.GitDiff(ctx)
	if err != nil {
		return result, err
	}
	repairs := 0
	madeMeaningfulEdit := false
	for step := 1; step <= maxSteps; step++ {
		result.StepsExecuted = step
		response, err := e.chatOnce(ctx, request.Model, messages)
		if err != nil {
			result.FinalSummary = err.Error()
			return result, err
		}
		messages = append(messages, response.Message)
		if len(response.Message.ToolCalls) > 0 {
			result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: StepToolCall})
			for _, call := range response.Message.ToolCalls {
				record := ToolCallRecord{Step: step, ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments.ToMap()}
				observation, toolErr := executeTool(ctx, workspace, call)
				if toolErr != nil {
					record.Error = toolErr.Error()
					observation = "error: " + toolErr.Error()
				} else {
					record.Output = observation
					if call.Function.Name == "apply_patch" {
						path, _ := stringArg(&call.Function.Arguments, "path")
						oldText, _ := stringArg(&call.Function.Arguments, "old_text")
						newText, _ := stringArg(&call.Function.Arguments, "new_text")
						meaningful := isMeaningfulSourceEdit(path, oldText, newText)
						madeMeaningfulEdit = madeMeaningfulEdit || meaningful
						if meaningful {
							result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: StepEditDetected, ToolName: call.Function.Name})
						}
					}
				}
				result.ToolCalls = append(result.ToolCalls, record)
				messages = append(messages, api.Message{Role: "tool", ToolName: call.Function.Name, ToolCallID: call.ID, Content: observation})
			}
			continue
		}
		kind := StepToolFreeEmpty
		if strings.TrimSpace(response.Message.Content) != "" {
			kind = StepToolFreeText
		}
		result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: kind})
		diff, changed, diffErr := workspace.GitDiff(ctx)
		if diffErr != nil {
			return result, diffErr
		}
		result.GitDiff, result.ChangedFiles = diff, changed
		if !madeMeaningfulEdit || diff == baselineDiff || !HasMeaningfulSourceDiff(diff) {
			if step < maxSteps {
				messages = append(messages, api.Message{Role: "user", Content: toolRetryPrompt})
				continue
			}
			result.FinalSummary = "maximum agent steps reached without a meaningful source diff"
			return result, nil
		}
		verification, passed := verifyAll(ctx, workspace, request.Verify)
		result.VerificationResults = verification
		passedCopy := passed
		result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: StepVerification, Passed: &passedCopy})
		if passed {
			result.Status = StatusSuccess
			result.FinalSummary = strings.TrimSpace(response.Message.Content)
			if result.FinalSummary == "" {
				result.FinalSummary = "source change verified"
			}
			return result, nil
		}
		if repairs >= MaxRepairAttempts {
			result.FinalSummary = "verification failed after maximum repair attempts"
			return result, nil
		}
		repairs++
		result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: StepRepair})
		encoded, _ := json.Marshal(verification)
		messages = append(messages, api.Message{Role: "user", Content: fmt.Sprintf("Verification failed (repair attempt %d of %d). Diagnose, edit with tools, inspect the diff, and retry. Results: %s", repairs, MaxRepairAttempts, encoded)})
	}
	result.FinalSummary = "maximum agent steps reached"
	diff, changed, _ := workspace.GitDiff(ctx)
	result.GitDiff, result.ChangedFiles = diff, changed
	return result, nil
}

func (e *Engine) chatOnce(ctx context.Context, model string, messages []api.Message) (api.ChatResponse, error) {
	stream := false
	req := &api.ChatRequest{Model: model, Messages: messages, Tools: agentTools(), Stream: &stream}
	var aggregate api.ChatResponse
	err := e.chat.Chat(ctx, req, func(part api.ChatResponse) error {
		content := aggregate.Message.Content
		toolCalls := aggregate.Message.ToolCalls
		aggregate = part
		aggregate.Message.Content = content
		aggregate.Message.ToolCalls = toolCalls
		if part.Message.Content != "" {
			aggregate.Message.Content += part.Message.Content
		}
		if len(part.Message.ToolCalls) > 0 {
			aggregate.Message.ToolCalls = append(aggregate.Message.ToolCalls, part.Message.ToolCalls...)
		}
		return nil
	})
	return aggregate, err
}

func stringArg(args *api.ToolCallFunctionArguments, name string) (string, error) {
	value, ok := args.Get(name)
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return s, nil
}

func executeTool(ctx context.Context, w *agenttools.Workspace, call api.ToolCall) (string, error) {
	args := &call.Function.Arguments
	switch call.Function.Name {
	case "search_files":
		q, _ := stringArg(args, "query")
		return w.SearchFiles(q, 100)
	case "read_file":
		p, err := stringArg(args, "path")
		if err != nil {
			return "", err
		}
		return w.ReadFile(p)
	case "apply_patch":
		p, err := stringArg(args, "path")
		if err != nil {
			return "", err
		}
		old, err := stringArg(args, "old_text")
		if err != nil {
			return "", err
		}
		newText, err := stringArg(args, "new_text")
		if err != nil {
			return "", err
		}
		return w.ApplyPatch(p, old, newText)
	case "shell":
		command, err := stringArg(args, "command")
		if err != nil {
			return "", err
		}
		r, err := w.RunShell(ctx, command, DefaultCommandTimeout, DefaultOutputLimit)
		b, _ := json.Marshal(r)
		return string(b), err
	case "git_diff":
		diff, _, err := w.GitDiff(ctx)
		return diff, err
	default:
		return "", fmt.Errorf("unknown tool %q", call.Function.Name)
	}
}

func verifyAll(ctx context.Context, w *agenttools.Workspace, commands []string) ([]VerificationResult, bool) {
	results := make([]VerificationResult, 0, len(commands))
	passed := true
	for _, command := range commands {
		r, err := w.RunShell(ctx, command, DefaultCommandTimeout, DefaultOutputLimit)
		vr := VerificationResult{Command: command, Passed: err == nil && r.ExitCode == 0, ExitCode: r.ExitCode, Stdout: r.Stdout, Stderr: r.Stderr, Truncated: r.Truncated}
		if !vr.Passed {
			passed = false
		}
		results = append(results, vr)
	}
	return results, passed
}
