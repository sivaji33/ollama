package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/pmezard/go-difflib/difflib"
)

var sensitiveAssignmentPattern = regexp.MustCompile(`(?i)^(\s*(?:export\s+)?["']?[\w.-]*(?:api[_-]?key|secret|password|passwd|token|credential|private[_-]?key|access[_-]?key|authorization)[\w.-]*["']?\s*[:=]\s*)(.*)$`)
var bearerTokenPattern = regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/-]+=*`)

type FilesystemChange struct {
	Path       string `json:"path"`
	OldPath    string `json:"old_path,omitempty"`
	Status     string `json:"status"`
	Before     string `json:"before,omitempty"`
	After      string `json:"after,omitempty"`
	Unified    string `json:"unified_diff"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	BeforeLine int    `json:"before_lines"`
	AfterLine  int    `json:"after_lines"`
}

type FilesystemDiff struct {
	Files     []FilesystemChange `json:"files"`
	Unified   string             `json:"unified_diff"`
	Additions int                `json:"additions"`
	Deletions int                `json:"deletions"`
}

func RedactFilesystemDiff(diff FilesystemDiff) FilesystemDiff {
	diff.Unified = ""
	for i := range diff.Files {
		change := &diff.Files[i]
		envFile := isEnvironmentFile(change.Path)
		change.Before = redactFileContent(change.Before, envFile)
		change.After = redactFileContent(change.After, envFile)
		change.Unified = redactUnifiedDiff(change.Unified, envFile)
		diff.Unified += change.Unified
	}
	return diff
}

func isEnvironmentFile(path string) bool {
	name := strings.ToLower(filepath.Base(filepath.FromSlash(path)))
	return name == ".env" || strings.HasPrefix(name, ".env.")
}

func redactFileContent(content string, envFile bool) string {
	if content == "" {
		return content
	}
	lines := strings.SplitAfter(content, "\n")
	for i, line := range lines {
		ending := ""
		if strings.HasSuffix(line, "\n") {
			ending = "\n"
			line = strings.TrimSuffix(line, ending)
		}
		lines[i] = redactContentLine(line, envFile) + ending
	}
	return strings.Join(lines, "")
}

func redactUnifiedDiff(diff string, envFile bool) string {
	lines := strings.SplitAfter(diff, "\n")
	for i, line := range lines {
		ending := ""
		if strings.HasSuffix(line, "\n") {
			ending = "\n"
			line = strings.TrimSuffix(line, ending)
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			lines[i] = "+" + redactContentLine(line[1:], envFile) + ending
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			lines[i] = "-" + redactContentLine(line[1:], envFile) + ending
		} else if strings.HasPrefix(line, " ") {
			lines[i] = " " + redactContentLine(line[1:], envFile) + ending
		}
	}
	return strings.Join(lines, "")
}

func redactContentLine(line string, envFile bool) string {
	if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return line
	}
	if envFile {
		if separator := strings.IndexByte(line, '='); separator >= 0 {
			return line[:separator+1] + "[REDACTED]"
		}
	}
	if match := sensitiveAssignmentPattern.FindStringSubmatch(line); len(match) == 3 {
		return match[1] + "[REDACTED]"
	}
	return bearerTokenPattern.ReplaceAllString(line, "${1}[REDACTED]")
}

type checkpointEntry struct {
	exists     bool
	backupPath string
	mode       os.FileMode
}

// FilesystemCheckpoint stores before-images only for paths the restricted
// workspace is about to mutate. This avoids copying generated build trees and
// repository metadata while still making every agent file operation reversible.
type FilesystemCheckpoint struct {
	root      string
	directory string
	mu        sync.Mutex
	files     map[string]checkpointEntry
	dirs      map[string]struct{}
	moves     map[string]string
	closed    bool
}

func NewFilesystemCheckpoint(root string) (*FilesystemCheckpoint, error) {
	workspace, err := NewRestrictedWorkspace(root)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "ownbot-selfdev-checkpoint-*")
	if err != nil {
		return nil, fmt.Errorf("create filesystem checkpoint: %w", err)
	}
	return &FilesystemCheckpoint{
		root:      workspace.Root(),
		directory: directory,
		files:     make(map[string]checkpointEntry),
		dirs:      make(map[string]struct{}),
		moves:     make(map[string]string),
	}, nil
}

func (c *FilesystemCheckpoint) BeforeMutation(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("filesystem checkpoint is closed")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absolute = filepath.Clean(absolute)
	rel, err := filepath.Rel(c.root, absolute)
	if err != nil {
		return err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("checkpoint path %q is outside the configured workspace", path)
	}
	key := filepath.ToSlash(rel)
	if _, saved := c.files[key]; saved {
		return nil
	}

	entry := checkpointEntry{}
	info, err := os.Stat(absolute)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint path %q is not a regular file", path)
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			return fmt.Errorf("read checkpoint source %q: %w", path, err)
		}
		backupPath := filepath.Join(c.directory, fmt.Sprintf("%x", sha256.Sum256([]byte(key))))
		if err := os.WriteFile(backupPath, content, 0o600); err != nil {
			return fmt.Errorf("save checkpoint source %q: %w", path, err)
		}
		entry = checkpointEntry{exists: true, backupPath: backupPath, mode: info.Mode().Perm()}
	case errors.Is(err, os.ErrNotExist):
	default:
		return fmt.Errorf("inspect checkpoint source %q: %w", path, err)
	}

	for parent := filepath.Dir(absolute); parent != c.root; parent = filepath.Dir(parent) {
		relDir, err := filepath.Rel(c.root, parent)
		if err != nil {
			return err
		}
		if _, err := os.Stat(parent); errors.Is(err, os.ErrNotExist) {
			c.dirs[filepath.ToSlash(relDir)] = struct{}{}
		} else if err != nil {
			return err
		}
		if filepath.Dir(parent) == parent {
			return fmt.Errorf("checkpoint path %q has no workspace parent", path)
		}
	}
	c.files[key] = entry
	return nil
}

func (c *FilesystemCheckpoint) BeforeMove(source, destination string) error {
	if err := c.BeforeMutation(source); err != nil {
		return err
	}
	if err := c.BeforeMutation(destination); err != nil {
		return err
	}
	sourceRel, err := filepath.Rel(c.root, source)
	if err != nil {
		return err
	}
	destinationRel, err := filepath.Rel(c.root, destination)
	if err != nil {
		return err
	}
	sourceKey, destinationKey := filepath.ToSlash(sourceRel), filepath.ToSlash(destinationRel)

	c.mu.Lock()
	defer c.mu.Unlock()
	for origin, target := range c.moves {
		if target == sourceKey {
			delete(c.moves, origin)
			sourceKey = origin
			break
		}
	}
	c.moves[sourceKey] = destinationKey
	return nil
}

func (c *FilesystemCheckpoint) Diff() (FilesystemDiff, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return FilesystemDiff{}, errors.New("filesystem checkpoint is closed")
	}
	paths := make([]string, 0, len(c.files))
	for path := range c.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	diff := FilesystemDiff{Files: make([]FilesystemChange, 0, len(paths))}
	changeByPath := make(map[string]int, len(paths))
	deletedByHash := make(map[string]int)
	addedByHash := make(map[string]int)
	for _, path := range paths {
		entry := c.files[path]
		currentPath := filepath.Join(c.root, filepath.FromSlash(path))
		afterBytes, readErr := os.ReadFile(currentPath)
		existsAfter := readErr == nil
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return FilesystemDiff{}, fmt.Errorf("read live file %q for diff: %w", path, readErr)
		}
		beforeBytes, err := c.beforeBytes(entry)
		if err != nil {
			return FilesystemDiff{}, err
		}
		if entry.exists == existsAfter && bytes.Equal(beforeBytes, afterBytes) {
			continue
		}
		change, err := makeFilesystemChange(path, beforeBytes, afterBytes, entry.exists, existsAfter)
		if err != nil {
			return FilesystemDiff{}, err
		}
		if change.Status == "deleted" {
			hash := sha256.Sum256(beforeBytes)
			deletedByHash[hex.EncodeToString(hash[:])] = len(diff.Files)
		} else if change.Status == "created" {
			hash := sha256.Sum256(afterBytes)
			addedByHash[hex.EncodeToString(hash[:])] = len(diff.Files)
		}
		changeByPath[path] = len(diff.Files)
		diff.Files = append(diff.Files, change)
	}

	moveSources := make([]string, 0, len(c.moves))
	for source := range c.moves {
		moveSources = append(moveSources, source)
	}
	sort.Strings(moveSources)
	for _, oldPath := range moveSources {
		newPath := c.moves[oldPath]
		sourceEntry, sourceTracked := c.files[oldPath]
		if !sourceTracked || !sourceEntry.exists {
			continue
		}
		sourceIndex, sourceChanged := changeByPath[oldPath]
		destinationIndex, destinationChanged := changeByPath[newPath]
		if !sourceChanged || !destinationChanged {
			continue
		}
		sourceChange, destinationChange := diff.Files[sourceIndex], diff.Files[destinationIndex]
		if sourceChange.Status != "deleted" || (destinationChange.Status != "created" && destinationChange.Status != "modified") {
			continue
		}
		destinationBytes, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(newPath)))
		if err != nil {
			return FilesystemDiff{}, fmt.Errorf("read moved file %q for diff: %w", newPath, err)
		}
		beforeBytes, err := c.beforeBytes(sourceEntry)
		if err != nil {
			return FilesystemDiff{}, err
		}
		change, err := makeFilesystemMove(oldPath, newPath, beforeBytes, destinationBytes)
		if err != nil {
			return FilesystemDiff{}, err
		}
		diff.Files[sourceIndex] = FilesystemChange{}
		diff.Files[destinationIndex] = change
	}

	for hash, deletedIndex := range deletedByHash {
		if diff.Files[deletedIndex].Path == "" {
			continue
		}
		addedIndex, ok := addedByHash[hash]
		if !ok {
			continue
		}
		if diff.Files[addedIndex].Path == "" {
			continue
		}
		oldPath := diff.Files[deletedIndex].Path
		newPath := diff.Files[addedIndex].Path
		change, err := makeFilesystemMove(oldPath, newPath, []byte(diff.Files[deletedIndex].Before), []byte(diff.Files[addedIndex].After))
		if err != nil {
			return FilesystemDiff{}, err
		}
		diff.Files[addedIndex] = change
		diff.Files[deletedIndex] = FilesystemChange{}
	}

	changes := diff.Files[:0]
	for _, change := range diff.Files {
		if change.Path != "" {
			changes = append(changes, change)
		}
	}
	diff.Files = changes
	for _, change := range diff.Files {
		diff.Unified += change.Unified
		diff.Additions += change.Additions
		diff.Deletions += change.Deletions
	}
	return diff, nil
}

func (c *FilesystemCheckpoint) beforeBytes(entry checkpointEntry) ([]byte, error) {
	if !entry.exists {
		return nil, nil
	}
	content, err := os.ReadFile(entry.backupPath)
	if err != nil {
		return nil, fmt.Errorf("read filesystem checkpoint before-image: %w", err)
	}
	return content, nil
}

func makeFilesystemChange(path string, before, after []byte, existedBefore, existsAfter bool) (FilesystemChange, error) {
	status := "modified"
	switch {
	case !existedBefore && existsAfter:
		status = "created"
	case existedBefore && !existsAfter:
		status = "deleted"
	}
	beforeText, afterText := string(before), string(after)
	beforeLines := splitDiffLines(beforeText)
	afterLines := splitDiffLines(afterText)
	unified, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        beforeLines,
		B:        afterLines,
		FromFile: "a/" + path,
		ToFile:   "b/" + path,
		Context:  3,
	})
	if err != nil {
		return FilesystemChange{}, fmt.Errorf("generate filesystem diff for %q: %w", path, err)
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "diff --git a/%s b/%s\n", path, path)
	if status == "created" {
		rendered.WriteString("new file mode 100644\n")
	}
	if status == "deleted" {
		rendered.WriteString("deleted file mode 100644\n")
	}
	rendered.WriteString(unified)
	additions, deletions := diffLineCounts(unified)
	return FilesystemChange{
		Path:       path,
		Status:     status,
		Before:     beforeText,
		After:      afterText,
		Unified:    rendered.String(),
		Additions:  additions,
		Deletions:  deletions,
		BeforeLine: len(beforeLines),
		AfterLine:  len(afterLines),
	}, nil
}

func makeFilesystemMove(oldPath, newPath string, before, after []byte) (FilesystemChange, error) {
	change, err := makeFilesystemChange(newPath, before, after, true, true)
	if err != nil {
		return FilesystemChange{}, err
	}
	beforeLines, afterLines := splitDiffLines(string(before)), splitDiffLines(string(after))
	unified, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        beforeLines,
		B:        afterLines,
		FromFile: "a/" + oldPath,
		ToFile:   "b/" + newPath,
		Context:  3,
	})
	if err != nil {
		return FilesystemChange{}, fmt.Errorf("generate move diff for %q: %w", newPath, err)
	}
	change.Status = "moved"
	change.OldPath = oldPath
	change.Before = string(before)
	change.Unified = fmt.Sprintf("diff --git a/%s b/%s\nrename from %s\nrename to %s\n%s", oldPath, newPath, oldPath, newPath, unified)
	change.Additions, change.Deletions = diffLineCounts(unified)
	change.BeforeLine, change.AfterLine = len(beforeLines), len(afterLines)
	return change, nil
}

func splitDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	lines[len(lines)-1] += "\n"
	return lines
}

func diffLineCounts(diff string) (int, int) {
	additions, deletions := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			additions++
		} else if strings.HasPrefix(line, "-") {
			deletions++
		}
	}
	return additions, deletions
}

func (c *FilesystemCheckpoint) Rollback() ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("filesystem checkpoint is closed")
	}
	paths := make([]string, 0, len(c.files))
	for path := range c.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	restored := make([]string, 0, len(paths))
	for _, path := range paths {
		entry := c.files[path]
		absolute := filepath.Join(c.root, filepath.FromSlash(path))
		if !entry.exists {
			if err := os.Remove(absolute); err != nil && !errors.Is(err, os.ErrNotExist) {
				return restored, fmt.Errorf("remove transaction-created file %q: %w", path, err)
			}
			restored = append(restored, path)
			continue
		}
		content, err := os.ReadFile(entry.backupPath)
		if err != nil {
			return restored, fmt.Errorf("read before-image for %q: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			return restored, fmt.Errorf("recreate parent directory for %q: %w", path, err)
		}
		if err := os.WriteFile(absolute, content, entry.mode); err != nil {
			return restored, fmt.Errorf("restore file %q: %w", path, err)
		}
		if err := os.Chmod(absolute, entry.mode); err != nil {
			return restored, fmt.Errorf("restore permissions for %q: %w", path, err)
		}
		restored = append(restored, path)
	}

	directories := make([]string, 0, len(c.dirs))
	for path := range c.dirs {
		directories = append(directories, path)
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, path := range directories {
		if err := os.Remove(filepath.Join(c.root, filepath.FromSlash(path))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return restored, fmt.Errorf("remove transaction-created directory %q: %w", path, err)
		}
	}
	if err := c.verifyRestored(); err != nil {
		return restored, err
	}
	return restored, nil
}

func (c *FilesystemCheckpoint) verifyRestored() error {
	for path, entry := range c.files {
		absolute := filepath.Join(c.root, filepath.FromSlash(path))
		if !entry.exists {
			if _, err := os.Lstat(absolute); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("rollback verification: transaction-created path %q still exists", path)
			}
			continue
		}
		want, err := os.ReadFile(entry.backupPath)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(absolute)
		if err != nil {
			return fmt.Errorf("rollback verification: read restored %q: %w", path, err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("rollback verification: restored content mismatch for %q", path)
		}
	}
	for path := range c.dirs {
		if _, err := os.Lstat(filepath.Join(c.root, filepath.FromSlash(path))); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rollback verification: transaction-created directory %q remains", path)
		}
	}
	return nil
}

func (c *FilesystemCheckpoint) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if err := os.RemoveAll(c.directory); err != nil {
		return fmt.Errorf("remove filesystem checkpoint: %w", err)
	}
	return nil
}
