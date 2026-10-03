# Self-Improve Loop

OwnBot can research the internet and improve its own source code by itself. The
loop picks a topic, gathers current knowledge with `web_search` and `web_fetch`,
edits this repository, and keeps the change **only if verification passes**.

```shell
ollama self-improve
```

By default it runs three cycles and stops when the hour budget is spent.

## Every cycle is guarded

An unattended loop that edits its own source has to be unable to leave the
repository broken. Commit `fa6dad5c` in this repository is the concrete example:
a half-finished merge was saved into the source and the project stopped
compiling. These gates exist to make that outcome impossible.

| Guard | What it prevents |
| --- | --- |
| **Checkpoint first** | The tree is committed *before* the cycle runs, so the whole cycle can be undone with one reset, including files git never tracked |
| **Independent verification** | The loop re-runs your verification commands itself. It never trusts the model's claim that its tests passed |
| **Conflict-marker guard** | A cycle that leaves `<<<<<<<` / `=======` / `>>>>>>>` behind is rejected and reverted — the exact defect that broke this repository |
| **Version-control guard** | A cycle that touches anything inside `.git` is rejected |
| **Compile gate** | `go build ./...` must pass, so a kept cycle can never leave the tree uncompilable |
| **Revert on failure** | Any failed gate resets the tree to the checkpoint and deletes files the cycle created, while leaving files you already had alone |
| **Dirty-tree refusal** | The loop refuses to start on a tree with uncommitted work, because reverting would otherwise discard it. Use `--allow-dirty` to have that work checkpointed first |
| **Budgets** | A cycle count and a wall-clock budget stop the loop |

## Options

| Flag | Default | Meaning |
| --- | --- | --- |
| `--model` | `qwen3:4b-instruct` | The model that performs each cycle |
| `--cycles` | `3` | How many improvement cycles to attempt |
| `--budget` | `1h` | Wall-clock limit for the whole session |
| `--verify` | `go build ./...` | Command that must pass for a cycle to be kept (repeatable) |
| `--build` | none | Extra command that must also pass, such as a full test run |
| `--topic` | six built-in topics | Research topic per cycle, in order (repeatable) |
| `--workspace` | current directory | The repository to improve |
| `--research-only` | off | Gather information and change nothing |
| `--allow-dirty` | off | Checkpoint uncommitted work instead of refusing |
| `--journal` | `~/.ollama/self-improve/journal.jsonl` | One JSON record per cycle |

## Start with research-only

Before letting it edit anything, watch what it actually finds:

```shell
ollama self-improve --research-only --cycles 3
```

Nothing is changed in this mode; the findings appear in the journal and on
screen. When the topics look useful, run the real thing:

```shell
ollama self-improve --cycles 1 --verify "go build ./..." --build "go test ./internal/agent/... -count=1"
```

One cycle at a time is the right way to start: check `git log` after each run to
see what it decided to change and why.

## Reading the result

Each cycle prints one line:

```
#1 kept     Improved error wrapping in the workspace reader | ...
     changed: internal/agent/tools/workspace.go
#2 reverted agent reported success without changing any file | ...
```

`kept` means the change passed every gate and is committed. `reverted` means a
gate rejected it and the tree was reset — the reason is always stated, and a
revert is a normal, safe outcome rather than a failure.

## Undoing a session

Every kept cycle is its own commit, so a session is easy to unwind:

```shell
git log --oneline --grep "self-improve"   # see what it changed
git revert <commit>                       # undo one kept cycle
git reset --hard <commit-before-session>  # undo the whole session
```

## What the loop does not do

It does **not** replace the running `ollama.exe`.

On Windows a running executable is locked, so a binary cannot be swapped
mid-flight; doing that safely needs the rename-then-restart dance that
`app/updater/updater_windows.go` uses for the official updater. That step is
deliberately left out of the loop: it is precisely the kind of unguarded
half-finished operation that broke this repository in `fa6dad5c`.

What you get instead is a **compile gate**. The default `--verify "go build
./..."` compiles every package and discards the output, so a kept cycle is
guaranteed to build without ever fighting the lock on the running binary.

To use the improved source, rebuild when you choose to:

```shell
go build .
```

## Requirements

* A git repository — the loop refuses to run without one, since checkpoints and
  reverts are how it undoes a cycle.
* A running Ollama server with the chosen model installed. Point the client at
  your runtime with `OLLAMA_HOST` if it is not on the default port.
* Internet access for the research step. Disable web tools with
  `OLLAMA_AGENT_WEB_DISABLED=1` and the cycles become repository-only work.