package agent

import "testing"

func TestMeaningfulSourceDiff(t *testing.T) {
	tests := []struct {
		name, diff string
		want       bool
	}{
		{"empty", "", false},
		{"whitespace", "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n- x := 1\n+  x := 1\n", false},
		{"comment", "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n+// gate comment\n", false},
		{"source", "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n-return 1\n+return 2\n", true},
		{"documentation", "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n-old\n+new\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasMeaningfulSourceDiff(tt.diff); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}
