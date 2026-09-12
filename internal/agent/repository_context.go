package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type RepositoryContextOptions struct {
	MaxTrackedFiles  int
	MaxRelevantFiles int
	MaxRecentFiles   int
}

type RepositoryContext struct {
	Workspace     string   `json:"workspace"`
	Branch        string   `json:"branch,omitempty"`
	GitStatus     []string `json:"git_status,omitempty"`
	TrackedFiles  []string `json:"tracked_files,omitempty"`
	RelevantFiles []string `json:"relevant_files,omitempty"`
	Manifests     []string `json:"manifests,omitempty"`
	Languages     []string `json:"languages,omitempty"`
	RecentFiles   []string `json:"recent_files,omitempty"`
}

type RepositoryContextBuilder struct {
	options RepositoryContextOptions
}

func NewRepositoryContextBuilder(options RepositoryContextOptions) *RepositoryContextBuilder {
	if options.MaxTrackedFiles <= 0 {
		options.MaxTrackedFiles = 200
	}
	if options.MaxRelevantFiles <= 0 {
		options.MaxRelevantFiles = 40
	}
	if options.MaxRecentFiles <= 0 {
		options.MaxRecentFiles = 40
	}

	return &RepositoryContextBuilder{options: options}
}

func (b *RepositoryContextBuilder) Build(
	ctx context.Context,
	workspace string,
	task string,
) (RepositoryContext, error) {
	var result RepositoryContext

	if b == nil {
		return result, errors.New("repository context builder is nil")
	}

	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return result, errors.New("workspace is required")
	}

	abs, err := filepath.Abs(workspace)
	if err != nil {
		return result, fmt.Errorf("resolve workspace: %w", err)
	}

	result.Workspace = abs

	branch, err := runGit(ctx, abs, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return result, err
	}
	result.Branch = strings.TrimSpace(branch)

	status, err := runGit(ctx, abs, "status", "--porcelain")
	if err != nil {
		return result, err
	}
	result.GitStatus = lines(status)

	tracked, err := runGit(ctx, abs, "ls-files")
	if err != nil {
		return result, err
	}

	allFiles := lines(tracked)
	for i := range allFiles {
		allFiles[i] = filepath.ToSlash(allFiles[i])
	}
	sort.Strings(allFiles)

	result.TrackedFiles = bounded(allFiles, b.options.MaxTrackedFiles)
	result.Manifests = findManifests(allFiles)
	result.Languages = findLanguages(allFiles)
	result.RelevantFiles = findRelevant(
		allFiles,
		task,
		b.options.MaxRelevantFiles,
	)
	result.RecentFiles = findRecent(
		result.GitStatus,
		b.options.MaxRecentFiles,
	)

	return result, nil
}

func runGit(
	ctx context.Context,
	workspace string,
	args ...string,
) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workspace

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		return "", fmt.Errorf(
			"git %s failed: %w: %s",
			strings.Join(args, " "),
			err,
			strings.TrimSpace(string(out)),
		)
	}

	return string(out), nil
}

func lines(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")

	var result []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}

	return result
}

func bounded(values []string, max int) []string {
	if max <= 0 || len(values) <= max {
		return append([]string(nil), values...)
	}

	return append([]string(nil), values[:max]...)
}

func findManifests(files []string) []string {
	names := map[string]bool{
		"go.mod":           true,
		"go.work":          true,
		"package.json":     true,
		"pyproject.toml":   true,
		"requirements.txt": true,
		"Cargo.toml":       true,
		"pom.xml":          true,
		"CMakeLists.txt":   true,
		"Makefile":         true,
	}

	var result []string

	for _, file := range files {
		if names[filepath.Base(filepath.FromSlash(file))] {
			result = append(result, file)
		}
	}

	sort.Strings(result)
	return result
}

func findLanguages(files []string) []string {
	found := map[string]bool{}

	for _, file := range files {
		switch strings.ToLower(filepath.Ext(file)) {
		case ".go":
			found["Go"] = true
		case ".py":
			found["Python"] = true
		case ".js", ".jsx", ".mjs", ".cjs":
			found["JavaScript"] = true
		case ".ts", ".tsx":
			found["TypeScript"] = true
		case ".rs":
			found["Rust"] = true
		case ".java":
			found["Java"] = true
		case ".c":
			found["C"] = true
		case ".cc", ".cpp", ".cxx", ".hpp":
			found["C++"] = true
		case ".cs":
			found["C#"] = true
		case ".ps1":
			found["PowerShell"] = true
		case ".sh":
			found["Shell"] = true
		}
	}

	var result []string
	for language := range found {
		result = append(result, language)
	}

	sort.Strings(result)
	return result
}

func findRelevant(files []string, task string, max int) []string {
	tokens := taskWords(task)

	type candidate struct {
		path  string
		score int
	}

	var candidates []candidate

	for _, file := range files {
		lower := strings.ToLower(file)
		base := strings.ToLower(filepath.Base(filepath.FromSlash(file)))

		score := 0
		for _, token := range tokens {
			if strings.Contains(base, token) {
				score += 4
			}
			if strings.Contains(lower, token) {
				score += 2
			}
		}

		if score > 0 {
			candidates = append(candidates, candidate{
				path:  file,
				score: score,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].score > candidates[j].score
	})

	var result []string
	for _, candidate := range candidates {
		if len(result) >= max {
			break
		}
		result = append(result, candidate.path)
	}

	return result
}

func taskWords(task string) []string {
	stop := map[string]bool{
		"the": true, "and": true, "for": true,
		"change": true, "modify": true, "update": true,
		"implementation": true, "source": true, "file": true,
	}

	seen := map[string]bool{}
	var result []string

	for _, word := range strings.FieldsFunc(
		strings.ToLower(task),
		func(r rune) bool {
			return !unicode.IsLetter(r) &&
				!unicode.IsDigit(r) &&
				r != '_' &&
				r != '-'
		},
	) {
		if len(word) < 2 || stop[word] || seen[word] {
			continue
		}

		seen[word] = true
		result = append(result, word)
	}

	sort.Strings(result)
	return result
}

func findRecent(status []string, max int) []string {
	seen := map[string]bool{}

	for _, line := range status {
		if len(line) < 3 {
			continue
		}

		path := strings.TrimSpace(line[2:])

		if pos := strings.LastIndex(path, " -> "); pos >= 0 {
			path = path[pos+4:]
		}

		path = strings.Trim(path, `"`)
		path = filepath.ToSlash(path)

		if path != "" {
			seen[path] = true
		}
	}

	var result []string
	for path := range seen {
		result = append(result, path)
	}

	sort.Strings(result)
	return bounded(result, max)
}
