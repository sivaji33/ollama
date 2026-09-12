package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func initContextRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}

	run("init")
	run("config", "user.name", "Context Test")
	run("config", "user.email", "context-test@localhost")

	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	write("go.mod", "module example.com/contexttest\n\ngo 1.25\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("internal/service.go", "package internal\n\nfunc Service() {}\n")
	write("README.md", "# Context Test\n")

	run("add", ".")
	run("commit", "-m", "baseline")

	return root
}

func TestRepositoryContextDetectsProjectMetadata(t *testing.T) {
	root := initContextRepo(t)

	builder := NewRepositoryContextBuilder(RepositoryContextOptions{
		MaxTrackedFiles:  100,
		MaxRelevantFiles: 20,
		MaxRecentFiles:   20,
	})

	got, err := builder.Build(
		context.Background(),
		root,
		"modify internal service",
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got.Workspace != root {
		t.Fatalf("Workspace = %q, want %q", got.Workspace, root)
	}

	if got.Branch == "" {
		t.Fatalf("Branch was empty")
	}

	if !containsString(got.Manifests, "go.mod") {
		t.Fatalf("Manifests = %#v, want go.mod", got.Manifests)
	}

	if !containsString(got.Languages, "Go") {
		t.Fatalf("Languages = %#v, want Go", got.Languages)
	}

	if !containsString(got.TrackedFiles, "main.go") {
		t.Fatalf("TrackedFiles = %#v, want main.go", got.TrackedFiles)
	}
}

func TestRepositoryContextFindsTaskRelevantFiles(t *testing.T) {
	root := initContextRepo(t)

	builder := NewRepositoryContextBuilder(RepositoryContextOptions{
		MaxTrackedFiles:  100,
		MaxRelevantFiles: 20,
		MaxRecentFiles:   20,
	})

	got, err := builder.Build(
		context.Background(),
		root,
		"change the service implementation",
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !containsString(got.RelevantFiles, "internal/service.go") {
		t.Fatalf(
			"RelevantFiles = %#v, want internal/service.go",
			got.RelevantFiles,
		)
	}
}

func TestRepositoryContextIncludesGitStatus(t *testing.T) {
	root := initContextRepo(t)

	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(
		path,
		[]byte("package main\n\nfunc main() { println(\"changed\") }\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	builder := NewRepositoryContextBuilder(RepositoryContextOptions{
		MaxTrackedFiles:  100,
		MaxRelevantFiles: 20,
		MaxRecentFiles:   20,
	})

	got, err := builder.Build(
		context.Background(),
		root,
		"change main",
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(got.GitStatus) == 0 {
		t.Fatalf("GitStatus unexpectedly empty")
	}

	if !containsString(got.RecentFiles, "main.go") {
		t.Fatalf("RecentFiles = %#v, want main.go", got.RecentFiles)
	}
}

func TestRepositoryContextIsDeterministic(t *testing.T) {
	root := initContextRepo(t)

	builder := NewRepositoryContextBuilder(RepositoryContextOptions{
		MaxTrackedFiles:  100,
		MaxRelevantFiles: 20,
		MaxRecentFiles:   20,
	})

	first, err := builder.Build(
		context.Background(),
		root,
		"modify internal service",
	)
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}

	second, err := builder.Build(
		context.Background(),
		root,
		"modify internal service",
	)
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf(
			"context not deterministic:\nfirst=%#v\nsecond=%#v",
			first,
			second,
		)
	}
}

func TestRepositoryContextRespectsBounds(t *testing.T) {
	root := initContextRepo(t)

	for i := 0; i < 20; i++ {
		name := filepath.Join(root, "generated", string(rune('a'+i))+".go")
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(name, []byte("package generated\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, output)
	}

	builder := NewRepositoryContextBuilder(RepositoryContextOptions{
		MaxTrackedFiles:  5,
		MaxRelevantFiles: 3,
		MaxRecentFiles:   2,
	})

	got, err := builder.Build(
		context.Background(),
		root,
		"generated go source",
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(got.TrackedFiles) > 5 {
		t.Fatalf("TrackedFiles length = %d, want <= 5", len(got.TrackedFiles))
	}

	if len(got.RelevantFiles) > 3 {
		t.Fatalf("RelevantFiles length = %d, want <= 3", len(got.RelevantFiles))
	}

	if len(got.RecentFiles) > 2 {
		t.Fatalf("RecentFiles length = %d, want <= 2", len(got.RecentFiles))
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
