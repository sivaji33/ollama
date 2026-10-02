# Agent Internet Knowledge: `web_search` + `web_fetch`

## Why

OwnBot's coding agent already edits files, runs the project's own tests, and
verifies its changes, but it could only use knowledge that was already in the
repository or inside the model. This capability lets the agent pull current
knowledge from the internet and use it to improve the workspace itself.

## What was added

- `web_search` — searches the internet and returns JSON with citation-ready
  `title`, `url`, and `snippet` fields for up to `max_results` results
  (default 5, maximum 10). Search redirect links are resolved to the real
  destination so results can be fetched directly.
- `web_fetch` — downloads an absolute `http(s)` URL and returns readable text
  (`title`, `content_type`, `text`); scripts, styles, and page boilerplate are
  removed before the text reaches the model.
- The agent system policy now instructs the model to research unfamiliar APIs
  and established best practices with these tools before editing.

## How the agent uses it

1. `web_search "<topic, API, or error message>"`.
2. `web_fetch` the most promising URLs for detail.
3. Change the repository with `apply_patch`, `multi_edit`, or `write_file`.
4. Run the project's focused tests with `shell`, inspect `git_diff`, and repair
   until verification passes.

This is the same self-upgrade loop the project already used, now with internet
knowledge inside the loop.

## Safety and budgets

- Result counts and snippet sizes are bounded: search responses are capped at
  512 KiB, each snippet at 480 characters, `web_fetch` reads at most 1 MiB per
  page and returns at most 16 KiB of text. Every tool observation the model
  sees is additionally clamped by the engine.
- Requests carry a fixed user agent, honor context cancellation, and time out
  after 20 seconds.
- `web_fetch` refuses non-`http(s)` schemes and blocks obvious internal hosts
  (localhost, loopback, private, and link-local addresses). This is a lexical
  host guard, not an operating-system firewall.
- `OLLAMA_AGENT_WEB_DISABLED=1` (or `true`) turns both tools off with a clear
  error for installations that prefer no outbound research. These tools talk to
  the open web directly rather than through Ollama cloud services, so they are
  unaffected by `OLLAMA_NO_CLOUD`.

## Configuration

- `OLLAMA_AGENT_WEB_SEARCH_URL` — optional. Points search at any
  DuckDuckGo-style HTML endpoint (for example a self-hosted SearXNG instance)
  for installations that prefer their own search backend. When unset, the
  agent uses DuckDuckGo's keyless HTML endpoint.
- `OLLAMA_AGENT_WEB_DISABLED` — optional kill switch. Set to `1` or `true` to
  disable `web_search` and `web_fetch`; leave unset to keep them enabled.

## Verification

```sh
go test ./internal/agent/... -count=1
```

The focused suite covers result parsing and redirect decoding, result-count
clamping, text extraction, URL and host rejection, response truncation, and an
end-to-end session where the agent gathers internet knowledge, edits the
workspace, and passes verification.
