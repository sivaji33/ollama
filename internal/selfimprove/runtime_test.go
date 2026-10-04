package selfimprove

import (
	"fmt"
	"strings"
	"testing"
)

func TestRuntimeEnvironmentEnforcesMinimumContext(t *testing.T) {
	tests := []struct {
		name            string
		current         []string
		expectedContext string
	}{
		{
			name:            "missing context",
			current:         []string{"PATH=C:\\bin"},
			expectedContext: fmt.Sprintf("OLLAMA_CONTEXT_LENGTH=%d", customRuntimeMinContext),
		},
		{
			name:            "context below minimum",
			current:         []string{"OLLAMA_CONTEXT_LENGTH=256"},
			expectedContext: fmt.Sprintf("OLLAMA_CONTEXT_LENGTH=%d", customRuntimeMinContext),
		},
		{
			name:            "invalid context",
			current:         []string{"OLLAMA_CONTEXT_LENGTH=invalid"},
			expectedContext: fmt.Sprintf("OLLAMA_CONTEXT_LENGTH=%d", customRuntimeMinContext),
		},
		{
			name:            "larger context preserved",
			current:         []string{"OLLAMA_CONTEXT_LENGTH=8192"},
			expectedContext: "OLLAMA_CONTEXT_LENGTH=8192",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := runtimeEnvironment(test.current)
			if count := countEnvironmentVariable(got, "OLLAMA_CONTEXT_LENGTH"); count != 1 {
				t.Fatalf("OLLAMA_CONTEXT_LENGTH entries = %d, want exactly 1: %v", count, got)
			}
			if !containsEnvironmentEntry(got, test.expectedContext) {
				t.Fatalf("environment does not contain %q: %v", test.expectedContext, got)
			}
			if !containsEnvironmentEntry(got, "OLLAMA_HOST=127.0.0.1:11435") {
				t.Fatalf("environment does not set the custom runtime host: %v", got)
			}
		})
	}
}

func countEnvironmentVariable(env []string, name string) int {
	count := 0
	for _, entry := range env {
		if key, _, ok := strings.Cut(entry, "="); ok && strings.EqualFold(key, name) {
			count++
		}
	}
	return count
}

func containsEnvironmentEntry(env []string, entry string) bool {
	for _, value := range env {
		if value == entry {
			return true
		}
	}
	return false
}
