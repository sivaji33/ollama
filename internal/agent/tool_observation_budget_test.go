package agent

import (
	"strings"
	"testing"
)

<<<<<<< HEAD
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
=======
func TestBoundedToolObservation(t *testing.T) {
	short := "small output"

>>>>>>> e2e7dd7cc6aae5bdeb13287ddc4c895899629a28
	if got := boundedToolObservation(short); got != short {
		t.Fatal("short output must remain unchanged")
	}

	large := "START" + strings.Repeat("x", 100000) + "END"
<<<<<<< HEAD
	got := boundedToolObservation(large)

	if len(got) > 4096 {
=======

	got := boundedToolObservation(large)

	if len(got) > maxToolObservationBytes {
>>>>>>> e2e7dd7cc6aae5bdeb13287ddc4c895899629a28
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
