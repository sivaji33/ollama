# Integrated Coding Agent Phase 1 Design

## Scope

Add a synchronous `POST /api/agent/run` vertical slice without changing existing inference, model loading, `/api/chat`, or `/api/generate` behavior. A request names a local model, workspace, coding task, bounded step count, and verification commands. The response records the session, tool activity, changed files, diff, verification, and a final summary.

## Architecture

`internal/agent` owns orchestration and public request/result types. Its model boundary consumes and returns Ollama `api.ChatRequest`/`api.ChatResponse`, including the existing `api.ToolCall` representation, so the agent adds no model protocol or parser. `server/agent.go` adapts the existing non-streaming `ChatHandler` to that boundary and registers the new route in `GenerateRoutes`.

`internal/agent/tools` owns workspace resolution and the agent's file, shell, and git tools. The workspace root supplies the default working directory and the base for relative paths, but it is deliberately not a containment boundary: absolute paths anywhere on the machine are permitted. Tool output and file reads are bounded.

The engine starts with a system policy and task message, calls the model with Ollama tool schemas, executes returned calls, appends the assistant tool-call message and tool-role observations, and repeats up to `max_steps` (default 12). Model prose never establishes success. For modification tasks, the engine requires a meaningful source diff, rejecting empty, whitespace-only, and comment-only changes. It then runs every requested verification command. A failed verification is returned to the model for at most two bounded repair rounds before final failure.

## Path and Shell Access

The workspace root is the working directory for commands and the base for relative paths, but tools are not restricted to it. Absolute paths, `..` traversal, directory changes, and symlinks anywhere on the machine resolve and execute normally, so the agent can also install, inspect, or update artifacts outside the repository, such as promoting a binary it just built. This is an explicit operator decision: the earlier lexical containment guard was removed, so there is no longer a filesystem boundary around the agent. Commands still run through the platform shell with context cancellation, a fixed timeout, bounded combined stdout/stderr, and a captured exit code.

## API Behavior

Invalid JSON or request validation returns HTTP 400. Engine/model/tool failures return a structured run result with a non-success status; unexpected server failures use HTTP 500. Successful modification requires both a meaningful diff and passing requested verification. The response includes `session_id`, `status`, `steps_executed`, `tool_calls`, `changed_files`, `git_diff`, `verification_results`, and `final_summary`.

## Testing

Tests use temporary workspaces and real filesystem, git, and shell behavior. A deterministic scripted chat client drives engine integration tests. Required red-green coverage includes reads and writes both inside and outside the workspace, real/no-op patches, meaningful-diff classification, shell execution and bounded output, verified completion, and rejection of model-declared success without a source diff. Server tests cover request validation and adapter/route behavior without loading a real model.
