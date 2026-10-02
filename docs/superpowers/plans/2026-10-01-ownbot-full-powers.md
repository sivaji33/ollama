# OwnBot Full Powers and Local-Only Registry

## Why

Two operator requests drove this change:

1. OwnBot must work completely under the operator's commands. The agent was
   still bounded by hard-coded engine budgets that could refuse or silently
   drop requested work (a tool-call budget, a fixed turn budget, a 12-message
   replay window, a 4 KiB observation clamp, a 2-minute command timeout, a
   64 KiB output cap, and a 64 KiB editor-context drop).
2. OwnBot must not connect to Ollama. The cloud, account, updater, and
   recommendation paths were already local-only, but model pulls and pushes
   still dialed `registry.ollama.ai` because the registry client only checked
   `OLLAMA_REMOTES` for remote-model *inference*, not for registry traffic.

## What changed

### Operator-controlled agent budgets

`internal/agent/limits.go` replaces the hard-coded engine constants with
environment variables. Unset or `0` means "no limit" for turns, tool calls per
turn, conversation replay, observation size, command timeout, output capture,
and editor context. The stagnation guard keeps its default of four no-progress
turns and can be disabled with `0`. `OLLAMA_AGENT_CONTEXT_WINDOW` keeps the
historical 12k default. `internal/agent/tools/shell.go` accepts negative
timeout and output limits as "no limit" and keeps the old conservative
defaults for zero values.

The agent system prompt now states that the operator owns the machine, has
authorized full access, and that the operator's instructions are authoritative
so the model executes the request instead of declining it.

### Local-only registry

`server/registry_policy.go` adds `allowRegistryHost`: registry traffic is
refused unless the target host is listed in `OLLAMA_REMOTES` or is a loopback
address. `PullModel` treats a fully local model as a successful no-op so
`ollama pull` keeps working offline, and `PushModel` requires an opted-in
registry host. `localhost`/`127.0.0.1` registries remain usable without any
opt-in.

### Documentation

`docs/local-only.md` documents the agent power knobs and the registry opt-in,
and the old `OLLAMA_CLOUD=1` example (which never existed) was replaced with
the real `OLLAMA_NO_CLOUD=0` switch.

## Verification

```sh
go test ./internal/agent/... ./server/ -count=1
```

Focused coverage:

- `internal/agent/limits_test.go` — defaults are unrestricted, env values are
  honored, every tool call in a turn executes by default, and an operator-set
  tool-call budget is enforced.
- `internal/agent/tool_observation_budget_test.go` — pass-through by default,
  truncation only when limited.
- `server/registry_policy_test.go` — default refusal, opt-in match,
  loopback exemption, pull/push refusal, and the local-model no-op.
- `server/agent_session_editor_context_limit_test.go` — editor context is
  passed through by default and dropped only when limited.
