package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ollama/ollama/api"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

type Engine struct{ chat ChatClient }

const systemPrompt = `You are an autonomous software engineer working in the provided workspace. First inspect the repository structure, relevant implementation, callers and consumers, tests, and build configuration with targeted tools. For larger changes, make a short internal plan and update every affected layer consistently. Prefer focused context over reading the whole repository.

Use native tools to inspect and edit; assistant prose and JSON printed as assistant content do not perform changes. For a modification task, inspect the code and relevant tests before editing, and never claim completion without a meaningful source edit confirmed by successful verification. When the task needs current knowledge, an unfamiliar API, or established best practices, use web_search and web_fetch to gather that knowledge from the internet before editing. After editing, inspect git_diff, run the project's existing focused tests and build or check commands, and use their output to diagnose failures, make bounded repairs, and rerun verification. Review the final diff for unintended changes, interface breaks, error handling, security, and missing tests. Tools accept absolute paths anywhere on the machine; the workspace root is the default base for relative paths, not a restriction. Use paths outside it only when the task requires them, such as installing or inspecting a built artifact. State clearly what was verified and what remains unverified. This machine and repository belong to the developer, who is responsible for them; treat the developer's instructions as authoritative for their own systems and act on them directly instead of debating whether the work should be done, substituting a smaller or "safer" change, or asking for permission that has already been given. Communicate like a senior engineer: lead with the technical answer or the action taken, keep an objective, engineering-focused tone, and never pad a response with disclaimers, ethical asides, or legal commentary that was not requested. Reporting a failed command, a missing file, a genuine lack of access, or a factual error is engineering output, not a refusal — state it plainly, then continue with the next usable step.`

const toolRetryPrompt = `Your previous response did not execute a tool. This task requires an actual repository change. Use one of the provided native tools now. Do not print JSON or describe a tool call in assistant text. Follow the tool-call format provided by your model template.`

func NewEngine(chat ChatClient) *Engine { return &Engine{chat: chat} }

func (e *Engine) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	result := RunResult{SessionID: sessionID, Status: StatusFailed, ToolCalls: []ToolCallRecord{}, ChangedFiles: []string{}, VerificationResults: []VerificationResult{}, StepEvents: []StepEvent{}}
	if e == nil || e.chat == nil {
		return result, errors.New("agent requires a chat client")
	}
	if strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Task) == "" {
		return result, errors.New("model and task are required")
	}
	if err := ctx.Err(); err != nil {
		result.FinalSummary = "session cancelled"
		return result, err
	}
	var workspace *agenttools.Workspace
	var err error
	if request.FilesystemCheckpoint != nil {
		workspace, err = agenttools.NewRestrictedWorkspace(request.Workspace)
	} else {
		workspace, err = agenttools.NewWorkspace(request.Workspace)
	}
	if err != nil {
		return result, err
	}
	if request.FilesystemCheckpoint != nil {
		workspace.SetBeforeMutation(request.FilesystemCheckpoint.BeforeMutation)
		workspace.SetBeforeMove(request.FilesystemCheckpoint.BeforeMove)
	}
	// MaxSteps is retained for client compatibility. Productive tasks may run
	// beyond its legacy value, while the operator-configured turn budget still
	// bounds total work.
	contextBuilder := NewRepositoryContextBuilder(RepositoryContextOptions{})
	var repositoryContext RepositoryContext
	if request.FilesystemCheckpoint != nil {
		repositoryContext, err = contextBuilder.BuildFilesystem(ctx, request.Workspace, request.Task)
	} else {
		repositoryContext, err = contextBuilder.Build(ctx, request.Workspace, request.Task)
	}
	if err != nil {
		return result, fmt.Errorf("build repository context: %w", err)
	}
	if err := ctx.Err(); err != nil {
		result.FinalSummary = "session cancelled"
		return result, err
	}

	result.ContextBuilt = true

	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
		{
			Role: "user",
			Content: request.Task +
				"\n\n" +
				formatRepositoryContext(repositoryContext),
		},
	}
	baselineDiff, _, err := workspaceDiff(ctx, workspace, request.FilesystemCheckpoint)
	if err != nil {
		return result, err
	}
	repairs := 0
	madeMeaningfulEdit := false
	editingStarted := false
	diffDetected := false
	repairPending := false
	stagnantTurns := 0
	seenObservations := make(map[[32]byte]struct{})
	seenSourceStates := make(map[[32]byte]struct{})
	verifiedSourceStates := make(map[[32]byte]struct{})
	maxTurns := agentTurnLimit()
	for step := 1; maxTurns <= 0 || step <= maxTurns; step++ {
		if err := ctx.Err(); err != nil {
			result.FinalSummary = "session cancelled"
			return result, err
		}
		result.StepsExecuted = step
		modelTurn := newLifecycleEvent(EventModelTurn, "model turn started")
		modelTurn.Step = request.StepOffset + step
		if err := emitLifecycleEvent(&result, request, modelTurn); err != nil {
			return result, fmt.Errorf("publish model-turn progress: %w", err)
		}
		response, err := e.chatOnce(ctx, request.Model, boundedAgentMessages(messages, step > 1), request.FilesystemCheckpoint != nil)
		if err != nil {
			result.FinalSummary = err.Error()
			return result, err
		}
		if err := ctx.Err(); err != nil {
			result.FinalSummary = "session cancelled"
			return result, err
		}
		messages = append(messages, response.Message)
		newObservation := false
		patchedThisTurn := false
		if len(response.Message.ToolCalls) > 0 {
			result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: StepToolCall})
			for callIndex, call := range response.Message.ToolCalls {
				if err := ctx.Err(); err != nil {
					result.FinalSummary = "session cancelled"
					return result, err
				}
				record := ToolCallRecord{Step: step, ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments.ToMap()}
				if limit := agentToolCallsPerTurnLimit(); limit > 0 && callIndex >= limit {
					record.Error = fmt.Sprintf("tool call budget exceeded: at most %d calls per model turn", limit)
					result.ToolCalls = append(result.ToolCalls, record)
					messages = append(messages, api.Message{Role: "tool", ToolName: call.Function.Name, ToolCallID: call.ID, Content: "error: " + record.Error})
					continue
				}
				toolStarted := newLifecycleEvent(EventToolCall, "executing tool "+call.Function.Name)
				toolStarted.Step = request.StepOffset + step
				toolStarted.ToolName = call.Function.Name
				toolStarted.Path = toolCallPath(call)
				if err := emitLifecycleEvent(&result, request, toolStarted); err != nil {
					return result, fmt.Errorf("publish tool-call progress: %w", err)
				}
				// Source-editing tools are evaluated before execution so that
				// write_file compares against the on-disk content it replaces.
				meaningfulEdit := false
				if isSourceEditorTool(call.Function.Name) {
					meaningfulEdit = callIsMeaningfulSourceEdit(workspace, call)
					if meaningfulEdit && !repairPending && !editingStarted {
						if err := emitLifecycleEvent(&result, request, newLifecycleEvent(EventEditingStarted, "editing started")); err != nil {
							return result, fmt.Errorf("publish lifecycle event %q: %w", EventEditingStarted, err)
						}
						editingStarted = true
					}
				}
				observation, toolErr := executeTool(ctx, workspace, call, request.FilesystemCheckpoint)
				if err := ctx.Err(); err != nil {
					result.FinalSummary = "session cancelled"
					return result, err
				}
				if toolErr != nil {
					record.Error = toolErr.Error()
					observation = "error: " + toolErr.Error()
				} else {
					record.Output = observation
					if isSourceEditorTool(call.Function.Name) {
						meaningful := meaningfulEdit
						madeMeaningfulEdit = madeMeaningfulEdit || meaningful
						patchedThisTurn = patchedThisTurn || meaningful
						if meaningful {
							result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: StepEditDetected, ToolName: call.Function.Name})
							if repairPending {
								if err := emitLifecycleEvent(&result, request, newLifecycleEvent(EventRepairCompleted, "repair completed")); err != nil {
									return result, fmt.Errorf("publish lifecycle event %q: %w", EventRepairCompleted, err)
								}
								repairPending = false
							}
						}
					} else {
						// A new, successful observation is useful once. Repeating the same
						// command, result and arguments is not forward progress.
						fingerprintInput, _ := json.Marshal(struct {
							Name      string
							Arguments map[string]any
							Output    string
						}{call.Function.Name, record.Arguments, boundedToolObservation(observation)})
						fingerprint := sha256.Sum256(fingerprintInput)
						if _, seen := seenObservations[fingerprint]; !seen {
							seenObservations[fingerprint] = struct{}{}
							newObservation = true
						}
					}
				}
				toolResult := "tool " + call.Function.Name + " completed"
				if toolErr != nil {
					toolResult = "tool " + call.Function.Name + " failed"
				}
				if toolErr == nil && isSourceEditorTool(call.Function.Name) {
					fileEvent := newLifecycleEvent(EventFilesystemChange, observation)
					fileEvent.Step = request.StepOffset + step
					fileEvent.ToolName = call.Function.Name
					fileEvent.Path = toolCallPath(call)
					if request.FilesystemCheckpoint != nil {
						liveDiff, diffErr := request.FilesystemCheckpoint.Diff()
						if diffErr != nil {
							return result, fmt.Errorf("collect live filesystem diff after %s: %w", call.Function.Name, diffErr)
						}
						fileEvent.FilesystemDiff = &liveDiff
					}
					if err := emitLifecycleEvent(&result, request, fileEvent); err != nil {
						return result, fmt.Errorf("publish file-change progress: %w", err)
					}
				}
				toolFinished := newLifecycleEvent(EventToolResult, toolResult)
				toolFinished.Step = request.StepOffset + step
				toolFinished.ToolName = call.Function.Name
				toolFinished.Path = toolCallPath(call)
				if err := emitLifecycleEvent(&result, request, toolFinished); err != nil {
					return result, fmt.Errorf("publish tool-result progress: %w", err)
				}
				result.ToolCalls = append(result.ToolCalls, record)
				messages = append(messages, api.Message{Role: "tool", ToolName: call.Function.Name, ToolCallID: call.ID, Content: boundedToolObservation(observation)})
			}
		} else {
			kind := StepToolFreeEmpty
			if strings.TrimSpace(response.Message.Content) != "" {
				kind = StepToolFreeText
			}
			result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: kind})
		}

		diff, changed, diffErr := workspaceDiff(ctx, workspace, request.FilesystemCheckpoint)
		if diffErr != nil {
			return result, diffErr
		}
		if err := ctx.Err(); err != nil {
			result.FinalSummary = "session cancelled"
			return result, err
		}
		result.GitDiff, result.ChangedFiles = diff, changed
		meaningfulDiff := madeMeaningfulEdit && diff != baselineDiff && HasMeaningfulSourceDiff(diff)
		if request.FilesystemCheckpoint != nil {
			meaningfulDiff = len(changed) > 0
		}
		sourceFingerprint := sha256.Sum256([]byte(diff))
		newSourceState := false
		if meaningfulDiff {
			if _, seen := seenSourceStates[sourceFingerprint]; !seen {
				seenSourceStates[sourceFingerprint] = struct{}{}
				newSourceState = true
			}
		}
		if newObservation || newSourceState {
			stagnantTurns = 0
		} else {
			stagnantTurns++
		}
		if limit := agentStagnationLimit(); limit > 0 && stagnantTurns >= limit {
			result.FinalSummary = fmt.Sprintf("agent stuck: %d consecutive turns without new observations or meaningful source changes", stagnantTurns)
			return result, nil
		}
		if request.FilesystemCheckpoint != nil {
			if len(response.Message.ToolCalls) == 0 && meaningfulDiff {
				result.Status = StatusSuccess
				result.FinalSummary = strings.TrimSpace(response.Message.Content)
				if result.FinalSummary == "" {
					result.FinalSummary = "source change verified"
				}
				return result, nil
			}
			if len(response.Message.ToolCalls) == 0 && !meaningfulDiff {
				messages = append(messages, api.Message{Role: "user", Content: toolRetryPrompt})
			}
			continue
		}
		if !meaningfulDiff || !patchedThisTurn {
			if len(response.Message.ToolCalls) == 0 && !meaningfulDiff {
				messages = append(messages, api.Message{Role: "user", Content: toolRetryPrompt})
			}
			continue
		}
		if _, alreadyVerified := verifiedSourceStates[sourceFingerprint]; alreadyVerified {
			continue
		}
		verifiedSourceStates[sourceFingerprint] = struct{}{}
		if !diffDetected {
			if err := emitLifecycleEvent(
				&result,
				request,
				newLifecycleEvent(
					EventDiffDetected,
					"meaningful source diff detected",
				),
			); err != nil {
				return result, fmt.Errorf(
					"publish lifecycle event %q: %w",
					EventDiffDetected,
					err,
				)
			}
			diffDetected = true
		}
		if err := emitLifecycleEvent(
			&result,
			request,
			newLifecycleEvent(
				EventVerificationStart,
				"verification started",
			),
		); err != nil {
			return result, fmt.Errorf(
				"publish lifecycle event %q: %w",
				EventVerificationStart,
				err,
			)
		}

		if err := ctx.Err(); err != nil {
			result.FinalSummary = "session cancelled"
			return result, err
		}
		verification, passed := verifyAll(ctx, workspace, request.Verify)
		if err := ctx.Err(); err != nil {
			result.FinalSummary = "session cancelled"
			return result, err
		}
		result.VerificationResults = verification
		passedCopy := passed
		result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: StepVerification, Passed: &passedCopy})
		if passed {
			if err := emitLifecycleEvent(
				&result,
				request,
				newLifecycleEvent(
					EventVerificationPassed,
					"verification passed",
				),
			); err != nil {
				return result, fmt.Errorf(
					"publish lifecycle event %q: %w",
					EventVerificationPassed,
					err,
				)
			}

			result.Status = StatusSuccess
			result.FinalSummary = strings.TrimSpace(response.Message.Content)
			if result.FinalSummary == "" {
				result.FinalSummary = "source change verified"
			}
			return result, nil
		}
		if err := emitLifecycleEvent(
			&result,
			request,
			newLifecycleEvent(
				EventVerificationFailed,
				"verification failed",
			),
		); err != nil {
			return result, fmt.Errorf(
				"publish lifecycle event %q: %w",
				EventVerificationFailed,
				err,
			)
		}

		repairs++
		repairPending = true
		if err := emitLifecycleEvent(
			&result,
			request,
			newLifecycleEvent(
				EventRepairStarted,
				"repair started",
			),
		); err != nil {
			return result, fmt.Errorf(
				"publish lifecycle event %q: %w",
				EventRepairStarted,
				err,
			)
		}
		result.StepEvents = append(result.StepEvents, StepEvent{Step: step, Kind: StepRepair})
		encoded, _ := json.Marshal(verification)
		messages = append(messages, api.Message{Role: "user", Content: fmt.Sprintf("Verification failed (repair attempt %d). Diagnose, make a new source change with tools, inspect the diff, and retry. Results: %s", repairs, encoded)})
	}
	if maxTurns > 0 {
		result.FinalSummary = fmt.Sprintf("agent attempt budget exhausted after %d model turns", maxTurns)
	} else {
		result.FinalSummary = "agent stopped before completing the task"
	}
	diff, changed, diffErr := workspaceDiff(ctx, workspace, request.FilesystemCheckpoint)
	if diffErr == nil {
		result.GitDiff, result.ChangedFiles = diff, changed
	}
	return result, nil
}

func boundedAgentMessages(messages []api.Message, followup bool) []api.Message {
	bounded := append([]api.Message(nil), messages...)
	if followup && len(bounded) > 1 {
		const repositoryMarker = "\n\nRepository context:\n"
		if before, _, ok := strings.Cut(bounded[1].Content, repositoryMarker); ok {
			bounded[1].Content = before + "\n\nRepository context was supplied on the first turn; use native tools for current repository state."
		}
	}
	limit := agentConversationLimit()
	if limit <= 0 {
		// The operator owns the context budget: replay the full history.
		return bounded
	}
	if limit < 2 {
		limit = 2
	}
	if len(bounded) <= limit {
		return bounded
	}
	// Keep assistant tool calls together with every corresponding tool result.
	// Trimming arbitrary messages can leave orphan tool results that Ollama
	// rejects or tool calls whose outputs have disappeared from the context.
	var turns [][]api.Message
	for i := 2; i < len(bounded); {
		start := i
		message := bounded[i]
		i++
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			complete := true
			for _, call := range message.ToolCalls {
				if i >= len(bounded) || bounded[i].Role != "tool" || bounded[i].ToolCallID != call.ID {
					complete = false
					break
				}
				i++
			}
			if !complete {
				// Drop an incomplete tool turn rather than sending malformed history.
				for i < len(bounded) && bounded[i].Role == "tool" {
					i++
				}
				continue
			}
		} else if message.Role == "tool" {
			// Tool messages are only valid as part of their assistant turn.
			continue
		}
		turns = append(turns, bounded[start:i])
	}

	result := make([]api.Message, 0, limit)
	result = append(result, bounded[0], bounded[1])
	var kept [][]api.Message
	remaining := limit - len(result)
	for i := len(turns) - 1; i >= 0; i-- {
		if len(turns[i]) > remaining {
			break
		}
		kept = append(kept, turns[i])
		remaining -= len(turns[i])
	}
	for i := len(kept) - 1; i >= 0; i-- {
		result = append(result, kept[i]...)
	}
	return result
}

func boundedToolObservation(value string) string {
	limit := agentObservationLimit()
	if limit <= 0 {
		// The operator owns the observation budget: pass tool output through.
		return value
	}
	if len(value) <= limit {
		return value
	}

	marker := fmt.Sprintf(
		"\n...[tool output truncated; original size: %d bytes]...\n",
		len(value),
	)

	budget := limit - len(marker)
	if budget < 2 {
		return strings.ToValidUTF8(value[:limit], "")
	}
	headSize := budget / 2
	tailSize := budget - headSize

	head := strings.ToValidUTF8(value[:headSize], "")
	tail := strings.ToValidUTF8(value[len(value)-tailSize:], "")

	return head + marker + tail
}
func emitLifecycleEvent(
	result *RunResult,
	request RunRequest,
	event SessionEvent,
) error {
	result.LifecycleEvents = append(
		result.LifecycleEvents,
		event,
	)

	if request.OnLifecycleEvent != nil {
		return request.OnLifecycleEvent(event)
	}

	return nil
}

func newLifecycleEvent(
	eventType SessionEventType,
	message string,
) SessionEvent {
	return SessionEvent{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Message:   message,
	}
}

func (e *Engine) chatOnce(ctx context.Context, model string, messages []api.Message, filesystemOnly bool) (api.ChatResponse, error) {
	stream := false
	truncate := true
	options := map[string]any{}
	if window := agentContextWindowLimit(); window > 0 {
		options["num_ctx"] = window
	}
	req := &api.ChatRequest{
		Model: model, Messages: messages, Tools: agentTools(filesystemOnly), Stream: &stream,
		Options: options, Truncate: &truncate,
	}
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

func toolCallPath(call api.ToolCall) string {
	switch call.Function.Name {
	case "move_file":
		source, _ := stringArg(&call.Function.Arguments, "source")
		destination, _ := stringArg(&call.Function.Arguments, "destination")
		return source + " -> " + destination
	default:
		path, _ := stringArg(&call.Function.Arguments, "path")
		if path == "" {
			path, _ = stringArg(&call.Function.Arguments, "pattern")
		}
		if path == "" {
			path, _ = stringArg(&call.Function.Arguments, "query")
		}
		return path
	}
}

// intFromToolArg reads a numeric tool argument. Depending on the model
// template, numbers may arrive as int, float64, or a string.
func intFromToolArg(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return n
		}
	}
	return 0
}

// isSourceEditorTool reports whether a tool changes file content in ways the
// engine's meaningful-source-edit gate must evaluate.
func isSourceEditorTool(name string) bool {
	switch name {
	case "apply_patch", "write_file", "multi_edit", "delete_file", "move_file":
		return true
	default:
		return false
	}
}

// callIsMeaningfulSourceEdit evaluates whether a source-editing tool call
// changes meaningful source content. It runs before tool execution so
// write_file compares against the on-disk content it will replace.
func callIsMeaningfulSourceEdit(w *agenttools.Workspace, call api.ToolCall) bool {
	path, _ := stringArg(&call.Function.Arguments, "path")
	switch call.Function.Name {
	case "apply_patch":
		oldText, _ := stringArg(&call.Function.Arguments, "old_text")
		newText, _ := stringArg(&call.Function.Arguments, "new_text")
		return isMeaningfulSourceEdit(path, oldText, newText)
	case "write_file":
		content, _ := stringArg(&call.Function.Arguments, "content")
		current, _ := w.ReadFile(path)
		return isMeaningfulSourceEdit(path, current, content)
	case "multi_edit":
		edits, err := parseEdits(&call.Function.Arguments)
		if err != nil {
			return false
		}
		for _, edit := range edits {
			if isMeaningfulSourceEdit(path, edit.OldText, edit.NewText) {
				return true
			}
		}
		return false
	case "delete_file":
		content, err := w.ReadFile(path)
		return err == nil && isMeaningfulSourceEdit(path, content, "")
	case "move_file":
		source, err := stringArg(&call.Function.Arguments, "source")
		if err != nil {
			return false
		}
		destination, err := stringArg(&call.Function.Arguments, "destination")
		if err != nil {
			return false
		}
		content, err := w.ReadFile(source)
		if err != nil {
			return false
		}
		return isMeaningfulSourceEdit(source, content, "") ||
			isMeaningfulSourceEdit(destination, "", content)
	default:
		return false
	}
}

// parseEdits decodes the multi_edit edits argument, which models may supply
// as a native array of objects or as a JSON-encoded string.
func parseEdits(args *api.ToolCallFunctionArguments) ([]agenttools.Edit, error) {
	value, ok := args.Get("edits")
	if !ok {
		return nil, errors.New("edits is required")
	}
	switch typed := value.(type) {
	case []any:
		return editsFromSlice(typed)
	case string:
		var decoded []any
		if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
			return nil, fmt.Errorf("edits must be an array of {old_text, new_text} objects: %w", err)
		}
		return editsFromSlice(decoded)
	default:
		return nil, errors.New("edits must be an array of {old_text, new_text} objects")
	}
}

func editsFromSlice(values []any) ([]agenttools.Edit, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one edit is required")
	}
	edits := make([]agenttools.Edit, 0, len(values))
	for i, value := range values {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edit %d must be an object", i+1)
		}
		oldText, ok := entry["old_text"].(string)
		if !ok {
			return nil, fmt.Errorf("edit %d old_text must be a string", i+1)
		}
		newText, ok := entry["new_text"].(string)
		if !ok {
			return nil, fmt.Errorf("edit %d new_text must be a string", i+1)
		}
		edits = append(edits, agenttools.Edit{OldText: oldText, NewText: newText})
	}
	return edits, nil
}

func executeTool(ctx context.Context, w *agenttools.Workspace, call api.ToolCall, checkpoint *agenttools.FilesystemCheckpoint) (string, error) {
	args := &call.Function.Arguments
	if checkpoint != nil && call.Function.Name == "shell" {
		return "", errors.New("shell execution is disabled during a filesystem-checkpointed self-development edit")
	}
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
		r, err := w.RunShell(ctx, command, agentCommandTimeout(), agentOutputLimit())
		b, _ := json.Marshal(r)
		return string(b), err
	case "write_file":
		p, err := stringArg(args, "path")
		if err != nil {
			return "", err
		}
		content, err := stringArg(args, "content")
		if err != nil {
			return "", err
		}
		return w.WriteFile(p, content)
	case "list_files":
		pattern, _ := stringArg(args, "pattern")
		return w.ListFiles(pattern, 100)
	case "delete_file":
		p, err := stringArg(args, "path")
		if err != nil {
			return "", err
		}
		return w.DeleteFile(p)
	case "move_file":
		source, err := stringArg(args, "source")
		if err != nil {
			return "", err
		}
		destination, err := stringArg(args, "destination")
		if err != nil {
			return "", err
		}
		return w.MoveFile(source, destination)
	case "multi_edit":
		p, err := stringArg(args, "path")
		if err != nil {
			return "", err
		}
		edits, err := parseEdits(args)
		if err != nil {
			return "", err
		}
		return w.MultiEdit(p, edits)
	case "git_diff":
		diff, _, err := workspaceDiff(ctx, w, checkpoint)
		return diff, err
	case "web_search":
		query, err := stringArg(args, "query")
		if err != nil {
			return "", err
		}
		maxResults := 0
		if value, ok := args.Get("max_results"); ok {
			maxResults = intFromToolArg(value)
		}
		return agenttools.WebSearch(ctx, query, maxResults)
	case "web_fetch":
		rawURL, err := stringArg(args, "url")
		if err != nil {
			return "", err
		}
		return agenttools.WebFetch(ctx, rawURL)
	default:
		return "", fmt.Errorf("unknown tool %q", call.Function.Name)
	}
}

func workspaceDiff(ctx context.Context, w *agenttools.Workspace, checkpoint *agenttools.FilesystemCheckpoint) (string, []string, error) {
	if checkpoint == nil {
		return w.GitDiff(ctx)
	}
	diff, err := checkpoint.Diff()
	if err != nil {
		return "", nil, err
	}
	changed := make([]string, 0, len(diff.Files)*2)
	for _, file := range diff.Files {
		if file.Status == "move_source" {
			changed = append(changed, file.Path)
			continue
		}
		if file.OldPath != "" {
			changed = append(changed, file.OldPath)
		}
		changed = append(changed, file.Path)
	}
	return diff.Unified, changed, nil
}

func verifyAll(ctx context.Context, w *agenttools.Workspace, commands []string) ([]VerificationResult, bool) {
	results := make([]VerificationResult, 0, len(commands))
	passed := true
	for _, command := range commands {
		r, err := w.RunShell(ctx, command, agentCommandTimeout(), agentOutputLimit())
		vr := VerificationResult{Command: command, Passed: err == nil && r.ExitCode == 0, ExitCode: r.ExitCode, Stdout: r.Stdout, Stderr: r.Stderr, Truncated: r.Truncated}
		if !vr.Passed {
			passed = false
		}
		results = append(results, vr)
	}
	return results, passed
}
