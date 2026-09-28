package agent

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

var sourceExtensions = map[string]bool{
	".c": true, ".cc": true, ".cpp": true, ".cs": true, ".go": true, ".h": true, ".hpp": true,
	".java": true, ".js": true, ".jsx": true, ".kt": true, ".php": true, ".py": true, ".rb": true,
	".rs": true, ".sh": true, ".swift": true, ".ts": true, ".tsx": true,
}

func HasMeaningfulSourceDiff(diff string) bool {
	var current string
	var removed, added []string
	meaningful := false
	flush := func() {
		if !isProductionSourcePath(current) {
			removed, added = nil, nil
			return
		}
		sort.Strings(removed)
		sort.Strings(added)
		if strings.Join(removed, "\n") != strings.Join(added, "\n") && (len(removed) > 0 || len(added) > 0) {
			meaningful = true
		}
		removed, added = nil, nil
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				current = strings.TrimPrefix(fields[3], "b/")
			}
			continue
		}
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") || len(line) == 0 {
			continue
		}
		if line[0] != '+' && line[0] != '-' {
			continue
		}
		value := strings.TrimSpace(line[1:])
		compact := strings.Join(strings.Fields(value), "")
		if compact == "" || strings.HasPrefix(compact, "//") || strings.HasPrefix(compact, "#") || strings.HasPrefix(compact, "/*") || strings.HasPrefix(compact, "*") || strings.HasSuffix(compact, "*/") {
			continue
		}
		if line[0] == '+' {
			added = append(added, compact)
		} else {
			removed = append(removed, compact)
		}
	}
	flush()
	return meaningful
}

func isMeaningfulSourceEdit(path, oldText, newText string) bool {
	if !isProductionSourcePath(path) {
		return false
	}
	normalize := func(text string) string {
		var kept []string
		for _, line := range strings.Split(text, "\n") {
			compact := strings.Join(strings.Fields(line), "")
			if compact == "" || strings.HasPrefix(compact, "//") || strings.HasPrefix(compact, "#") || strings.HasPrefix(compact, "/*") || strings.HasPrefix(compact, "*") || strings.HasSuffix(compact, "*/") {
				continue
			}
			kept = append(kept, compact)
		}
		return strings.Join(kept, "\n")
	}
	return normalize(oldText) != normalize(newText)
}

func isProductionSourcePath(path string) bool {
	normalizedPath := filepath.ToSlash(filepath.Clean(path))
	parts := strings.Split(strings.ToLower(normalizedPath), "/")
	for _, part := range parts[:len(parts)-1] {
		if isTestDirectory(part) || isTemporaryPathComponent(part) {
			return false
		}
	}

	base := parts[len(parts)-1]
	originalBase := filepath.Base(normalizedPath)
	if isTestSourceName(base) || isConventionallyNamedTestSource(originalBase) || isTemporaryPathComponent(base) {
		return false
	}
	return sourceExtensions[strings.ToLower(filepath.Ext(base))]
}

func isTestDirectory(name string) bool {
	switch name {
	case "test", "tests", "__tests__", "spec", "specs":
		return true
	default:
		return false
	}
}

func isTestSourceName(name string) bool {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	return strings.HasPrefix(stem, "test_") ||
		strings.HasPrefix(stem, "test-") ||
		strings.HasSuffix(stem, "_test") ||
		strings.HasSuffix(stem, "_tests") ||
		strings.HasSuffix(stem, "_spec") ||
		strings.HasSuffix(stem, "_specs") ||
		strings.HasSuffix(stem, ".test") ||
		strings.HasSuffix(stem, ".tests") ||
		strings.HasSuffix(stem, ".spec") ||
		strings.HasSuffix(stem, ".specs")
}

func isConventionallyNamedTestSource(name string) bool {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if strings.HasSuffix(stem, "Test") ||
		strings.HasSuffix(stem, "Tests") ||
		strings.HasSuffix(stem, "TestCase") ||
		strings.HasSuffix(stem, "TestCases") {
		return true
	}
	if strings.HasPrefix(stem, "Test") {
		for _, r := range strings.TrimPrefix(stem, "Test") {
			return unicode.IsUpper(r)
		}
	}
	return false
}

func isTemporaryPathComponent(name string) bool {
	if name == "tmp" || name == "temp" || name == "temporary" || strings.HasSuffix(name, "~") {
		return true
	}
	for _, suffix := range []string{".tmp", ".temp", ".bak", ".backup", ".orig", ".rej", ".swp", ".swo", ".part"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	for _, prefix := range []string{".agent-edit-", ".agent-write-", ".tmp-", ".temp-", "tmp-", "temp-", "tmp_", "temp_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return strings.Contains(name, ".tmp.") || strings.Contains(name, ".temp.")
}
