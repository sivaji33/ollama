package agent

import (
	"path/filepath"
	"sort"
	"strings"
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
		if !sourceExtensions[strings.ToLower(filepath.Ext(current))] {
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
	if !sourceExtensions[strings.ToLower(filepath.Ext(path))] {
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
