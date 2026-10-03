package selfimprove

import "strings"

// DefaultTopics steer each cycle at a capability the operator asked the loop to
// grow: coding skill and reasoning skill. The built-in topics ask for research
// when external guidance is part of the requested improvement.
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
// rules require the agent to complete every requested operation before its
// final response and independent verification.
func buildTask(topic string, verify []string, researchOnly bool) string {
	var b strings.Builder

	if researchOnly {
		b.WriteString("Research cycle. Do not modify any file in this cycle.\n\n")
	} else {
		b.WriteString("Complete the requested improvement across every necessary file before finishing. Make the smallest coherent change, but do not stop after the first successful edit if the task has more requested operations.\n\n")
	}

	b.WriteString("Topic for this cycle:\n")
	b.WriteString(topic)
	b.WriteString("\n\nRules for this cycle:\n")
	b.WriteString("1. Inspect the relevant workspace files first. Use web_search and web_fetch only when current external information is necessary.\n")
	if researchOnly {
		b.WriteString("2. Change nothing. Report findings that a later cycle can act on.\n")
	} else {
		b.WriteString("2. Complete all requested file changes using workspace file tools; shell execution is unavailable. The transaction controller runs the configured tests and build after all edits.\n")
	}
	b.WriteString("3. Never modify anything inside .git or leave conflict markers behind.\n")
	b.WriteString("4. Only claim changes that are reflected in the live filesystem. If you cannot complete the task, state what remains.\n")

	if len(verify) > 0 && !researchOnly {
		b.WriteString("\nAfter all requested edits, the transaction controller will run these independent gates:\n")
		for _, command := range verify {
			b.WriteString("- " + command + "\n")
		}
	}
	return b.String()
}
