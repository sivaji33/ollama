# Integrated Coding Agent Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a synchronous, workspace-restricted coding-agent endpoint using Ollama's existing chat/tool-call contract.

**Architecture:** A focused engine depends on an injected Ollama chat boundary and a registry of canonical-root tools. A thin server adapter invokes the existing `ChatHandler` non-streaming, preserving its model scheduling and tool parsing.

**Tech Stack:** Go 1.26, Gin, Ollama `api` types, OS filesystem/process APIs, Git CLI.

**Spec:** `docs/superpowers/specs/2026-09-12-agent-phase-1-design.md`

## Global Constraints

- Preserve existing inference, model loading, `/api/chat`, and `/api/generate` behavior.
- Default to 12 agent steps and allow at most 2 verification repair attempts.
- Treat a missing meaningful source diff as failure for modification tasks.
- Treat the shell restriction as a fail-closed lexical guard, not an OS sandbox.

---

### Task 1: Workspace and file tools

**Files:** Create `internal/agent/tools/workspace.go`, `read_file.go`, `search_files.go`, `apply_patch.go`, and focused tests.

**Interfaces:** Produce `NewWorkspace(root string)`, safe path resolution, `ReadFile`, `SearchFiles`, and `ApplyPatch` operations returning bounded observations.

- [ ] Write tests that reject sibling paths, traversal, and symlink escapes; read a real file; create a real exact-text edit; and reject no-op replacement.
- [ ] Run `go test ./internal/agent/tools/...` and confirm failures are caused by missing production APIs.
- [ ] Implement canonical containment, bounded reads/search, and atomic exact-text patching.
- [ ] Re-run the focused tests and retain only behavior-focused cases.

### Task 2: Shell, git diff, and meaningful-change policy

**Files:** Create `internal/agent/tools/shell.go`, platform helpers if needed, `git_diff.go`, and tests; create `internal/agent/diff.go` and tests.

**Interfaces:** Produce bounded `Shell.Run(ctx, command)`, `GitDiff`, changed-file extraction, and `HasMeaningfulSourceDiff(diff string)`.

- [ ] Write tests for successful workspace execution, exit codes, output truncation, cancellation/timeout, and rejection of outside absolute paths/traversal/directory changes.
- [ ] Write literal diff fixtures proving empty, whitespace-only, and comment-only diffs fail while a source-token change passes.
- [ ] Run focused tests and witness expected failures.
- [ ] Implement the conservative command guard, cross-platform process execution, bounded output, git operations, and diff classifier.
- [ ] Re-run focused tests.

### Task 3: Agent session and engine

**Files:** Create `internal/agent/session.go`, `engine.go`, `schema.go`, and tests.

**Interfaces:** Consume a `ChatClient` using `api.ChatRequest` and `api.ChatResponse`; produce JSON-ready `RunRequest`, `RunResult`, step/tool/verification records.

- [ ] Write scripted-client tests for a real edit plus passing verification and for rejection when the model stops without a meaningful source diff.
- [ ] Add tests for default/max step limits and no more than two repair rounds after failed verification.
- [ ] Run focused engine tests and witness missing-feature failures.
- [ ] Implement the inspect/model/tool loop using `api.Tool`, `api.ToolCall`, and tool-role `api.Message` values.
- [ ] Implement engine-owned success gates, verification execution, observations, and deterministic limits.
- [ ] Re-run all `internal/agent/...` tests.

### Task 4: Server adapter and route

**Files:** Create `server/agent.go` and `server/agent_test.go`; modify `server/routes.go`; add API types to `api/types.go` only if the public boundary requires them.

**Interfaces:** Register `POST /api/agent/run`; adapt a non-streaming `api.ChatRequest` to the unchanged `ChatHandler` and return its parsed `api.ChatResponse`.

- [ ] Write handler tests for malformed/invalid requests and a deterministic injected engine path.
- [ ] Run server-focused tests and witness expected failures.
- [ ] Implement the handler, adapter, and one route registration without altering existing endpoint handlers.
- [ ] Re-run server-focused and route tests.

### Task 5: Formatting and verification

**Files:** All files above.

- [ ] Run `gofmt` on every changed Go file.
- [ ] Run `go test ./internal/agent/...`.
- [ ] Run relevant server tests.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...` and report any failures exactly.
- [ ] Run `go build .` and the repository CMake build required by `AGENTS.md` when prerequisites are available.
- [ ] Run `git diff --check`, `git diff --stat`, and inspect the final diff for unintended endpoint/inference changes.
