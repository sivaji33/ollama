package agent

import (
	"strings"
	"testing"
)

func TestBoundedToolObservationUnrestrictedByDefault(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_OBSERVATION_BYTES", "")

	large := "START" + strings.Repeat("x", 100000) + "END"
	if got := boundedToolObservation(large); got != large {
		t.Fatalf("unrestricted observation = %d bytes, want %d", len(got), len(large))
	}
}

func TestBoundedToolObservationRespectsOperatorLimit(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_OBSERVATION_BYTES", "4096")

	short := "small output"
	if got := boundedToolObservation(short); got != short {
		t.Fatal("short output must remain unchanged")
	}

	large := "START" + strings.Repeat("x", 100000) + "END"
	got := boundedToolObservation(large)

	if len(got) > 4096 {
		t.Fatalf("output size = %d; exceeds limit", len(got))
	}

	if !strings.HasPrefix(got, "START") {
		t.Fatal("beginning of output was lost")
	}

	if !strings.HasSuffix(got, "END") {
		t.Fatal("end of output was lost")
	}

	if !strings.Contains(got, "truncated") {
		t.Fatal("missing truncation notice")
	}
}
