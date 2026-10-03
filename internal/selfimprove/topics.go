package selfimprove

import "strings"

// DefaultTopics steer each cycle at a capability the operator asked the loop to
// grow: coding skill and reasoning skill. The topics are framed as research
// questions rather than instructions, because the agent's own system prompt
// expects it to research first with web_search and web_fetch and change this
// repository second.
//
// Each topic is deliberately narrow. A cycle that tries to do several things at
// once produces a change that is hard to verify and hard to revert.
func DefaultTopics() []string {
	return []string{
		"Coding: research current authoritative guidance on Go error handling and error wrapping, then apply the single most valuable improvement you find to this repository's own error handling.",
		"Thinking: research current prompting and reasoning techniques for small local models (task decomposition, self-verification, admitting uncertainty), then improve this repository's agent system prompt with the strongest technique you find.",
		"Testing: research modern Go table-driven test practice and edge-case selection, then strengthen the weakest existing test in a package this repository's agent depends on.",
		"Reasoning: research how tool-calling agents recover from failed tool calls and stuck loops, then improve this repository's agent loop only where the improvement is provably safer.",
		"Correctness: research common Go concurrency defects (context cancellation, leaked goroutines, data races), then fix one real instance in this repository if and only if you can prove it with a test.",
		"Coding: research idiomatic ways to make Go code easier to review and reason about (small functions, explicit interfaces, clear names), then apply one focused refactor to this repository.",
	}
}

// buildTask turns one topic into the task text for one cycle. The operating
// rules are in the brief itself because an unattended loop that edits its own
// source has to be told explicitly what it must never leave behind.
func buildTask(topic string, verify []string, researchOnly bool) string {
	var b strings.Builder

	if researchOnly {
		b.WriteString("Research cycle. Do not modify any file in this cycle.\n\n")
	} else {
		b.WriteString("You are improving your own source repository, one small and fully verified change at a time.\n\n")
	}

	b.WriteString("Topic for this cycle:\n")
	b.WriteString(topic)
	b.WriteString("\n\nRules for this cycle:\n")
	b.WriteString("1. Research first with web_search and web_fetch, and cite what you learned in your final summary.\n")
	if researchOnly {
		b.WriteString("2. Change nothing. Report findings that a later cycle can act on.\n")
	} else {
		b.WriteString("2. Make the smallest change that delivers the improvement. One concern per cycle.\n")
		b.WriteString("3. Run the focused tests for every package you touch, not the whole repository.\n")
	}
	b.WriteString("4. Never leave an unfinished merge, conflict marker, or placeholder behind.\n")
	b.WriteString("5. Never modify anything inside .git, and never delete a file you did not create.\n")
	b.WriteString("6. If you cannot make a safe verified improvement, change nothing and say so plainly.\n")

	if len(verify) > 0 && !researchOnly {
		b.WriteString("\nThe change must keep every one of these commands passing:\n")
		for _, command := range verify {
			b.WriteString("- " + command + "\n")
		}
	}
	return b.String()
}
