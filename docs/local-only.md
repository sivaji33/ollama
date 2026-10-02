# Local-Only Mode

OwnBot is a fork of Ollama that runs entirely on your machine. It does not
connect to any official Ollama service (model registry, account/auth API,
hosted web search, hosted model recommendations, or the release/updates
channel) unless you explicitly opt in.

Your general internet access is unaffected: models can still search the web and
fetch pages, and the server still serves the local API over `127.0.0.1`.

## What is off by default

| Capability | Default behavior |
| --- | --- |
| Registry pulls and pushes (`ollama pull gemma3`) | Refused: no registry host is enabled, so only a model already stored locally can be "pulled" (as a no-op). Loopback registries are always reachable |
| Cloud model inference (`gemma3:cloud`) | Rejected with `ollama cloud is disabled: remote model is unavailable` |
| Account sign-in and `/api/me`, account cloud model list | Skipped; sign-in reports that it is unavailable |
| Hosted web search proxy | Replaced by the built-in keyless internet search |
| Hosted model recommendations | Disabled (returns no recommendations) |
| Desktop updater (`/api/update`) | Update check is skipped, so the app never replaces itself with an official build |
| Request signing for `ollama.com` | The client only signs for `ollama.com` when cloud is explicitly enabled |

## Turning cloud on

Cloud features are opt-in. Enable them before launching OwnBot with
`OLLAMA_NO_CLOUD=0`, or with `"disable_ollama_cloud": false` in
`~/.ollama/server.json`.

The desktop app also exposes the same switch in its settings, which is stored
in the app database and takes precedence over the ambient environment for the
process it launches.

Registry traffic is gated separately by `OLLAMA_REMOTES`, which has no default
value in this fork. To use a registry of your own, or the official one, set it
(comma separated):

```shell
OLLAMA_REMOTES=myregistry.local:5000 ollama serve
```

```shell
OLLAMA_REMOTES=registry.ollama.ai ollama serve   # official registry, opt-in
```

## Internet tools (always available)

Web tools never require an account, an API key, or an Ollama service.

* **Web search** uses a keyless DuckDuckGo HTML query. Point it at your own
  provider (for example a self-hosted SearXNG instance) with:

  ```shell
  OLLAMA_WEB_SEARCH_URL=http://searxng.internal:8080/search
  ```

  The built-in parser accepts SearXNG's HTML/JSON response shapes, plus
  DuckDuckGo and Bing HTML layouts. An `Authorization` header can be supplied
  with `OLLAMA_WEB_SEARCH_API_KEY` if your provider requires one.

* **Web fetch** downloads and extracts the page (text, title, and links)
  directly, with redirects limited and private/loopback addresses blocked by
  default to keep server-side tools from reaching internal networks.

* The HTTP API endpoints `/api/experimental/web_search` and
  `/api/experimental/web_fetch` are answered locally by the same code, so
  clients such as the Claude API compatibility layer work without a proxy.

## Agent powers and limits

OwnBot's agent runs with the operator's authority on the operator's machine.
The workspace is a working directory, not a sandbox: file tools accept
absolute paths anywhere and shell commands may touch anything the user
account can. Engine budgets are operator-controlled rather than hard-coded;
unset or `0` means "no limit".

| Environment variable | Default | Effect |
| --- | --- | --- |
| `OLLAMA_AGENT_MAX_TURNS` | unlimited | Model turns per run |
| `OLLAMA_AGENT_TOOL_CALLS_PER_TURN` | unlimited | Tool calls executed from a single model response |
| `OLLAMA_AGENT_STAGNANT_TURNS` | `4` | Consecutive no-progress turns before the run stops; `0` disables the guard |
| `OLLAMA_AGENT_CONTEXT_WINDOW` | `12288` | `num_ctx` for agent chat requests; `0` lets the model default apply |
| `OLLAMA_AGENT_CONVERSATION_MESSAGES` | unlimited | Messages replayed to the model |
| `OLLAMA_AGENT_OBSERVATION_BYTES` | unlimited | Tool output bytes passed back to the model |
| `OLLAMA_AGENT_COMMAND_TIMEOUT` | no timeout | Seconds before a shell command is killed |
| `OLLAMA_AGENT_OUTPUT_LIMIT` | unlimited | Captured stdout/stderr bytes per command |
| `OLLAMA_AGENT_EDITOR_CONTEXT_BYTES` | unlimited | Editor context accepted from the client before it is dropped |
| `OLLAMA_AGENT_WEB_DISABLED` | web tools on | Set to `1` to turn `web_search` and `web_fetch` off |

For example, to bound a run to 30 minutes per command and 512 KiB of captured
output while leaving everything else unlimited:

```shell
OLLAMA_AGENT_COMMAND_TIMEOUT=1800 OLLAMA_AGENT_OUTPUT_LIMIT=524288 ollama serve
```

## Verifying that nothing leaves the machine

The `api/endpoints.go` helper and the fail-closed cloud policy make the default
state verifiable: with no cloud-related environment variables set,

```shell
go test ./internal/cloud/ ./envconfig/ ./api/ ./anthropic/ ./app/tools/ ./server/
```

covers the default-off behavior (including the registry guard in
`server/registry_policy_test.go`), and `ss -tnp` (or Resource Monitor) shows no
connections to `ollama.com` while `ollama serve` is idle.
