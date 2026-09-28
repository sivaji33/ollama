package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ollama/ollama/api"
)

func TestEngineFirstTurnUsesBoundedRepositoryContext(t *testing.T) {
	root := initRepo(t)

	if err := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte("module example.com/agentcontext\n\ngo 1.25\n"),
		0o600,
	); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := os.MkdirAll(
		filepath.Join(root, "internal"),
		0o700,
	); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(root, "internal", "service.go"),
		[]byte("package internal\n\n// VERY_SECRET_SOURCE_BODY\nfunc Service() {}\n"),
		0o600,
	); err != nil {
		t.Fatalf("write service.go: %v", err)
	}

	for _, argv := range [][]string{
		{"add", "."},
		{"commit", "-m", "add context fixtures"},
	} {
		cmd := exec.Command("git", argv...)
		cmd.Dir = root

		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf(
				"git %v: %v\n%s",
				argv,
				err,
				output,
			)
		}
	}

	chat := &scriptedChat{
		responses: []api.ChatResponse{
			{
				Message: api.Message{
					Role:    "assistant",
					Content: "done",
				},
			},
			{
				Message: api.Message{
					Role:    "assistant",
					Content: "still done",
				},
			},
			{
				Message: api.Message{
					Role:    "assistant",
					Content: "done again",
				},
			},
			{
				Message: api.Message{
					Role:    "assistant",
					Content: "done again",
				},
			},
		},
	}

	result, err := NewEngine(chat).Run(
		context.Background(),
		RunRequest{
			Model:     "test",
			Workspace: root,
			Task:      "modify service behavior",
			MaxSteps:  1,
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// This test is about first-turn context, not successful completion.
	if result.Status == StatusSuccess {
		t.Fatalf("unexpected success without source edit")
	}
	if !result.ContextBuilt {
		t.Fatalf("real engine did not report repository context as built")
	}

	// MaxSteps is deprecated; unproductive turns end at the stagnation limit.
	if len(chat.requests) != 4 {
		t.Fatalf(
			"chat requests = %d, want 4",
			len(chat.requests),
		)
	}

	messages := chat.requests[0].Messages

	if len(messages) < 2 {
		t.Fatalf("first request messages = %#v", messages)
	}

	prompt := messages[1].Content

	required := []string{
		"Repository context:",
		"Branch:",
		"Manifests:",
		"go.mod",
		"Languages:",
		"Go",
		"Tracked files:",
		"Relevant files:",
		"internal/service.go",
	}

	for _, want := range required {
		if !strings.Contains(prompt, want) {
			t.Fatalf(
				"first-turn prompt missing %q:\n%s",
				want,
				prompt,
			)
		}
	}

	if strings.Contains(prompt, "Initial workspace file listing:") {
		t.Fatalf(
			"legacy workspace listing still present:\n%s",
			prompt,
		)
	}

	// Repository context is metadata. It must not dump source bodies.
	if strings.Contains(prompt, "VERY_SECRET_SOURCE_BODY") {
		t.Fatalf(
			"repository context leaked source contents:\n%s",
			prompt,
		)
	}
}
