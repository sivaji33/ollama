import { describe, expect, it, vi } from "vitest";
import { act, create } from "react-test-renderer";
import { Message as MessageType } from "@/gotypes";
import Message from "./Message";

vi.mock("./StreamingMarkdownContent", () => ({
  default: () => null,
}));

describe("Message agent file changes", () => {
  it("renders the real filename and unified diff in an expandable block", () => {
    const message = new MessageType({
      role: "tool",
      content: "Agent file changes",
      tool_name: "agent_file_changes",
      tool_result: {
        files: [
          {
            path: "src/real-file.ts",
            status: "modified",
            unified_diff:
              "--- a/src/real-file.ts\n+++ b/src/real-file.ts\n-oldCode()\n+realChangedCode()\n",
            additions: 1,
            deletions: 1,
          },
        ],
      },
    });

    let renderer: ReturnType<typeof create>;
    act(() => {
      renderer = create(<Message message={message} isStreaming={false} />);
    });
    const block = renderer.root.findByProps({
      "data-testid": "agent-file-changes",
    });
    const summaryText = renderer.root
      .findAllByType("span")
      .map((span) => span.children.join(""))
      .join(" ");
    const diff = renderer.root.findByType("pre");

    expect(block).toBeTruthy();
    expect(summaryText).toContain("Modified");
    expect(summaryText).toContain("src/real-file.ts");
    expect(diff.children.join("")).toContain("+realChangedCode()");
    expect(renderer.root.findByType("details")).toBeTruthy();
  });
});
