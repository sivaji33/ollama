# Live Self-Development

OwnBot can use its configured local Ollama Codex runtime to edit the live
workspace. Each cycle checkpoints files before agent-tool mutations, calculates
the resulting diff from disk, runs independent verification and an optional
build, then retains the changes or restores the checkpoint.

```shell
ollama self-improve --cycles 1 --topic "Improve a focused part of the code"
```

## Transaction flow

1. Verify the configured executable, its process on `127.0.0.1:11435`, runtime
   identity, `qwen3:4b-instruct`, and a real inference request.
2. Create a filesystem checkpoint in a temporary directory. Before each
   restricted workspace file operation, save that path's original contents,
   existence, and permissions.
3. Let the local agent inspect and edit files directly in the configured live
   workspace. Writes are checked by rereading the files from disk. Self-
   development does not expose arbitrary shell execution to the model.
4. Generate the authoritative diff from checkpoint before-images and current
   filesystem contents, not from model claims or Git.
5. Reject failed agent runs, no-op edits, conflict markers, or edits to Git
   internals. Run the configured verification commands and optional build.
6. Retain verified file changes, or restore and verify the checkpoint on disk.

Git is not required. Existing Git metadata protections run only when a `.git`
entry exists; source changes are never committed, pushed, or reset by this
transaction. Verification and build commands are operator-configured commands
run in the workspace; keep them focused on tests/builds and do not use them for
untracked source mutations.

## Options

| Flag | Default | Meaning |
| --- | --- | --- |
| `--model` | `qwen3:4b-instruct` | Required local model |
| `--cycles` | `3` | Number of transactions |
| `--budget` | `1h` | Wall-clock limit |
| `--verify` | `go build ./...` | Independent command that must pass (repeatable) |
| `--build` | none | Additional build command that must pass |
| `--topic` | built-in topics | Task for a cycle (repeatable) |
| `--workspace` | current directory | Live workspace to edit |
| `--research-only` | off | Research without retaining edits |
| `--journal` | `~/.ollama/self-improve/journal.jsonl` | JSONL transaction journal |

The legacy `--allow-dirty` flag is accepted for compatibility. Dirty Git state
does not block a transaction because rollback restores only paths changed by
the transaction, not the rest of the workspace.

## Live Development UI

The desktop sidebar's **Live Development** page starts, polls, and cancels
local self-development transactions. Its timeline and expandable before/after
and unified diff views are populated by backend runtime, agent, filesystem,
verification, build, retention, and rollback events.
