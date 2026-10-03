package selfimprove

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ConflictMarker is an unfinished merge saved into a file: a "<<<<<<<" line,
// the "=======" divider, and a ">>>>>>>" line. That is exactly the defect that
// broke this repository in commit fa6dad5c, where a half-finished merge was
// written into the source and stopped the project from building.
//
// A self-modifying loop is the thing most likely to reproduce that defect, so
// the loop treats a conflicted file as a failed cycle and reverts it.
type ConflictMarker struct {
	Path string
	Line int
}

// findConflictMarkers returns the first conflict marker in each file that
// contains a complete marker block. The three markers must appear in order with
// a bare "=======" between them, so ordinary data that merely contains runs of
// equals signs -- tokenizer fixtures, for example -- is not mistaken for an
// unfinished merge.
func findConflictMarkers(root string, files []string) []ConflictMarker {
	var found []ConflictMarker
	for _, file := range files {
		start, ok := firstConflictBlock(filepath.Join(root, filepath.FromSlash(file)))
		if !ok {
			continue
		}
		found = append(found, ConflictMarker{Path: filepath.ToSlash(file), Line: start})
	}
	return found
}

func firstConflictBlock(path string) (int, bool) {
	file, err := os.Open(path)
	if err != nil {
		// A file that cannot be read (deleted, or a directory) has no marker
		// block to report.
		return 0, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	start := 0
	divider := false
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		switch {
		case strings.HasPrefix(text, "<<<<<<< "):
			// A new opening marker restarts the search: any block before it was
			// incomplete.
			start, divider = line, false
		case start > 0 && !divider && text == "=======":
			divider = true
		case start > 0 && divider && strings.HasPrefix(text, ">>>>>>> "):
			return start, true
		}
	}
	return 0, false
}
