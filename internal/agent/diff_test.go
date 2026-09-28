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
		{"go test source", "diff --git a/a_test.go b/a_test.go\n--- a/a_test.go\n+++ b/a_test.go\n-return 1\n+return 2\n", false},
		{"typescript test source", "diff --git a/button.test.tsx b/button.test.tsx\n--- a/button.test.tsx\n+++ b/button.test.tsx\n-return 1\n+return 2\n", false},
		{"java test source", "diff --git a/ServiceTest.java b/ServiceTest.java\n--- a/ServiceTest.java\n+++ b/ServiceTest.java\n-return 1\n+return 2\n", false},
		{"csharp test source", "diff --git a/ServiceTests.cs b/ServiceTests.cs\n--- a/ServiceTests.cs\n+++ b/ServiceTests.cs\n-return 1\n+return 2\n", false},
		{"tests directory", "diff --git a/tests/feature.go b/tests/feature.go\n--- a/tests/feature.go\n+++ b/tests/feature.go\n-return 1\n+return 2\n", false},
		{"temporary extension", "diff --git a/main.go.tmp b/main.go.tmp\n--- a/main.go.tmp\n+++ b/main.go.tmp\n-return 1\n+return 2\n", false},
		{"temporary source suffix", "diff --git a/main.tmp.go b/main.tmp.go\n--- a/main.tmp.go\n+++ b/main.tmp.go\n-return 1\n+return 2\n", false},
		{"temporary edit file", "diff --git a/.agent-edit-123.go b/.agent-edit-123.go\n--- a/.agent-edit-123.go\n+++ b/.agent-edit-123.go\n-return 1\n+return 2\n", false},
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

func TestMeaningfulSourceEditRequiresProductionSource(t *testing.T) {
	tests := []struct {
		name, path string
		want       bool
	}{
		{"production source", "internal/agent/engine.go", true},
		{"go test source", "internal/agent/engine_test.go", false},
		{"typescript test source", "ui/button.test.tsx", false},
		{"java test source", "src/ServiceTest.java", false},
		{"csharp test source", "src/ServiceTests.cs", false},
		{"tests directory", "tests/agent.go", false},
		{"temporary extension", "internal/agent/engine.go.tmp", false},
		{"temporary source suffix", "internal/agent/engine.tmp.go", false},
		{"temporary edit file", "internal/agent/.agent-edit-123.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMeaningfulSourceEdit(tt.path, "return 1", "return 2"); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}
