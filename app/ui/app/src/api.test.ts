import { afterEach, describe, expect, it, vi } from "vitest";
const { listModels } = vi.hoisted(() => ({ listModels: vi.fn() }));
vi.mock("./lib/ollama-client", () => ({
  ollamaClient: { list: listModels },
}));

import {
  fetchConnectUrl,
  getClaudeDesktopAvailableModels,
  getIntegrationStatuses,
  sendAgentMessage,
} from "./api";

describe("fetchConnectUrl", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("requests a desktop handoff after account creation", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            signin_url:
              "https://ollama.com/connect?name=MacBook&key=public-key",
          }),
          { status: 401 },
        ),
      ),
    );

    await expect(fetchConnectUrl()).resolves.toBe(
      "https://ollama.com/connect?name=MacBook&key=public-key&launch=true",
    );
  });
});

describe("getIntegrationStatuses", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns desktop and launcher integration metadata", async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            id: "claude-desktop",
            name: "Claude",
            description: "Use Ollama models in Claude Desktop",
            installed: true,
          },
          {
            id: "opencode",
            name: "OpenCode",
            description: "Open-source coding agent",
            command: "ollama launch opencode",
          },
        ]),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetch);

    await expect(getIntegrationStatuses()).resolves.toEqual([
      {
        id: "claude-desktop",
        name: "Claude",
        description: "Use Ollama models in Claude Desktop",
        installed: true,
      },
      {
        id: "opencode",
        name: "OpenCode",
        description: "Open-source coding agent",
        command: "ollama launch opencode",
      },
    ]);
    expect(fetch).toHaveBeenCalledWith(
      "http://127.0.0.1:3001/api/v1/integrations",
    );
  });
});

describe("sendAgentMessage", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("streams the real filename and diff before the session finishes", async () => {
    let sessionState = "RUNNING";
    const file = {
      path: "src/live-change.ts",
      status: "modified",
      unified_diff:
        "--- a/src/live-change.ts\n+++ b/src/live-change.ts\n-oldValue()\n+newValue()\n",
      additions: 1,
      deletions: 1,
    };
    const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/api/v1/create-chat")) {
        return new Response(JSON.stringify({ id: "chat-1" }), { status: 200 });
      }
      if (url.endsWith("/api/agent/session") && init?.method === "POST") {
        return new Response(
          JSON.stringify({ id: "session-1", state: "RUNNING" }),
          { status: 200 },
        );
      }
      if (url.endsWith("/api/agent/session/session-1/events")) {
        return new Response(
          JSON.stringify([
            {
              type: "filesystem_change",
              filesystem_diff: { files: [file] },
            },
          ]),
          { status: 200 },
        );
      }
      if (url.endsWith("/api/agent/session/session-1")) {
        if (sessionState === "RUNNING") {
          sessionState = "VERIFIED";
          return new Response(
            JSON.stringify({ id: "session-1", state: "RUNNING" }),
            { status: 200 },
          );
        }
        return new Response(
          JSON.stringify({
            id: "session-1",
            state: sessionState,
            final_summary: "Changes verified.",
          }),
          { status: 200 },
        );
      }
      if (url.endsWith("/agent-message")) {
        return new Response(null, { status: 204 });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal("fetch", fetch);

    const events = sendAgentMessage("new", "edit the file", "test-model", "C:/repo");
    expect((await events.next()).value?.eventName).toBe("chat_created");
    const fileEvent = (await events.next()).value;
    expect(fileEvent?.eventName).toBe("agent_file_change");
    if (fileEvent?.eventName !== "agent_file_change") {
      throw new Error("expected live agent file-change event");
    }
    expect(fileEvent.agentFileChanges?.[0]).toMatchObject({
      path: "src/live-change.ts",
      unified_diff: expect.stringContaining("+newValue()"),
    });
    expect(sessionState).toBe("RUNNING");

    const summary = (await events.next()).value;
    expect(summary?.eventName).toBe("chat");
    expect(sessionState).toBe("VERIFIED");
  });
});

describe("getClaudeDesktopAvailableModels", () => {
  afterEach(() => {
    listModels.mockReset();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("returns installed local models while pruning remote entries", async () => {
    listModels.mockResolvedValue({
      models: [
        { name: "llama3.2:latest", digest: "local" },
        {
          name: "remote-placeholder",
          digest: "remote",
          remote_host: "https://ollama.com",
        },
      ],
    });
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);

    const models = await getClaudeDesktopAvailableModels();

    expect(models.map((model) => model.model)).toEqual(["llama3.2"]);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("does not request cloud models when they are unavailable to the user", async () => {
    listModels.mockResolvedValue({
      models: [
        { name: "qwen3:8b", digest: "local" },
        { name: "deepseek-v4-flash:cloud", digest: "cached-cloud" },
        { name: "gemma4:31b-cloud", digest: "legacy-cached-cloud" },
      ],
    });
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);

    const models = await getClaudeDesktopAvailableModels();

    expect(models.map((model) => model.model)).toEqual(["qwen3:8b"]);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("loads the account cloud list in parallel when Cloud is available", async () => {
    listModels.mockResolvedValue({
      models: [{ name: "qwen3:8b", digest: "local" }],
    });
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          models: [
            { name: "glm-5.2", digest: "cloud" },
            { name: "gemma4:31b-cloud", digest: "legacy-cloud" },
            { name: "qwen3:8b", digest: "cloud-duplicate" },
          ],
        }),
      ),
    );
    vi.stubGlobal("fetch", fetch);

    const models = await getClaudeDesktopAvailableModels(true);

    expect(models.map((model) => model.model)).toEqual([
      "qwen3:8b",
      "glm-5.2:cloud",
      "gemma4:31b-cloud",
    ]);
    expect(fetch).toHaveBeenCalledWith(
      "http://127.0.0.1:3001/api/v1/models/cloud",
    );
  });

  it("keeps local models when the account cloud list fails", async () => {
    listModels.mockResolvedValue({
      models: [{ name: "qwen3:8b", digest: "local" }],
    });
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));

    const models = await getClaudeDesktopAvailableModels(true);

    expect(models.map((model) => model.model)).toEqual(["qwen3:8b"]);
  });
});
