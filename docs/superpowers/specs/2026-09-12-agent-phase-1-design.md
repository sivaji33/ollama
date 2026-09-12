# Integrated Coding Agent Phase 1 Design

## Scope

Add a synchronous `POST /api/agent/run` vertical slice without changing existing inference, model loading, `/api/chat`, or `/api/generate` behavior. A request names a local model, workspace, coding task, bounded step count, and verification commands. The response records the session, tool activity, changed files, diff, verification, and a final summary.

## Architecture

`internal/agent` owns orchestration and public request/result types. Its model boundary consumes and returns Ollama `api.ChatRequest`/`api.ChatResponse`, including the existing `api.ToolCall` representation, so the agent adds no model protocol or parser. `server/agent.go` adapts the existing non-streaming `ChatHandler` to that boundary and registers the new route in `GenerateRoutes`.

`internal/agent/tools` owns canonical workspace resolution and five tools: file search/list, file read, exact patch application, workspace-restricted shell execution, and git diff. File paths are independently resolved and checked beneath a canonical workspace root, with symlink escape checks. Tool output and file reads are bounded.

The engine starts with a system policy and task message, calls the model with Ollama tool schemas, executes returned calls, appends the assistant tool-call message and tool-role observations, and repeats up to `max_steps` (default 12). Model prose never establishes success. For modification tasks, the engine requires a meaningful source diff, rejecting empty, whitespace-only, and comment-only changes. It then runs every requested verification command. A failed verification is returned to the model for at most two bounded repair rounds before final failure.

## Shell Boundary

Commands run through the platform shell with the canonical workspace as `cwd`, context cancellation, a fixed timeout, bounded combined stdout/stderr, and captured exit code. A conservative lexical guard rejects explicit absolute paths outside the workspace, escaping `..` traversal, and obvious directory changes outside the workspace. This is deliberately documented as a command guard, not an operating-system sandbox.

## API Behavior

Invalid JSON or request validation returns HTTP 400. Engine/model/tool failures return a structured run result with a non-success status; unexpected server failures use HTTP 500. Successful modification requires both a meaningful diff and passing requested verification. The response includes `session_id`, `status`, `steps_executed`, `tool_calls`, `changed_files`, `git_diff`, `verification_results`, and `final_summary`.

## Testing

Tests use temporary workspaces and real filesystem, git, and shell behavior. A deterministic scripted chat client drives engine integration tests. Required red-green coverage includes workspace escape rejection, reads, real/no-op patches, meaningful-diff classification, shell execution and rejection, verified completion, and rejection of model-declared success without a source diff. Server tests cover request validation and adapter/route behavior without loading a real model.
