package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepositoryContextSurfacesAdvancedMetadata(t *testing.T) {
	root := initContextRepo(t)

	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	write("main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestMain(t *testing.T) {}\n")
	write("docs/architecture.md", "# Architecture\n")
	write("docs/notes.rst", "Notes\n")
	write("cmd/tool/main.go", "package main\n\nfunc main() {}\n")

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, output)
	}

	builder := NewRepositoryContextBuilder(RepositoryContextOptions{
		MaxTrackedFiles:  100,
		MaxRelevantFiles: 20,
		MaxRecentFiles:   20,
		MaxTestFiles:     20,
		MaxDocFiles:      20,
		MaxTopDirs:       20,
	})

	got, err := builder.Build(context.Background(), root, "update service")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !containsString(got.TestFiles, "main_test.go") {
		t.Fatalf("TestFiles = %#v, want main_test.go", got.TestFiles)
	}
	if !containsString(got.DocFiles, "README.md") || !containsString(got.DocFiles, "docs/architecture.md") {
		t.Fatalf("DocFiles = %#v", got.DocFiles)
	}
	if !containsString(got.TopDirs, "internal") || !containsString(got.TopDirs, "cmd") || !containsString(got.TopDirs, "docs") {
		t.Fatalf("TopDirs = %#v", got.TopDirs)
	}

	if len(got.TestFiles) > 20 || len(got.DocFiles) > 20 || len(got.TopDirs) > 20 {
		t.Fatalf("advanced metadata exceeded bounds: %#v", got)
	}
}
