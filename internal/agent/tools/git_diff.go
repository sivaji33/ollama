package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (w *Workspace) GitDiff(ctx context.Context) (string, []string, error) {
	const diffLimit = 512 * 1024
	result, err := w.RunShell(ctx, "git diff --no-ext-diff -- .", 30*time.Second, 512*1024)
	if err != nil {
		return "", nil, err
	}
	if result.ExitCode != 0 {
		return "", nil, &CommandError{Result: result}
	}
	names, err := w.RunShell(ctx, "git diff --name-only -- .", 30*time.Second, 64*1024)
	if err != nil {
		return "", nil, err
	}
	var changed []string
	for _, name := range strings.Split(strings.TrimSpace(names.Stdout), "\n") {
		name = strings.TrimSpace(name)
		if name != "" {
			changed = append(changed, name)
		}
	}
	untracked, err := w.RunShell(ctx, "git ls-files --others --exclude-standard -- .", 30*time.Second, 64*1024)
	if err != nil {
		return "", nil, err
	}
	for _, name := range strings.Split(strings.TrimSpace(untracked.Stdout), "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		content, readErr := w.ReadFile(filepath.FromSlash(name))
		if readErr != nil {
			continue
		}
		changed = append(changed, name)
		lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		addition := fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, name, name, len(lines))
		for _, line := range lines {
			addition += "+" + line + "\n"
		}
		remaining := diffLimit - len(result.Stdout)
		if remaining <= 0 {
			break
		}
		if len(addition) > remaining {
			addition = addition[:remaining]
		}
		result.Stdout += addition
	}
	return result.Stdout, changed, nil
}

type CommandError struct{ Result ShellResult }

func (e *CommandError) Error() string { return "command exited unsuccessfully" }
