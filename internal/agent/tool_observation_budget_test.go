package agent

import (
	"strings"
	"testing"
)

func TestBoundedToolObservation(t *testing.T) {
	short := "small output"

	if got := boundedToolObservation(short); got != short {
		t.Fatal("short output must remain unchanged")
	}

	large := "START" + strings.Repeat("x", 100000) + "END"

	got := boundedToolObservation(large)

	if len(got) > maxToolObservationBytes {
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
