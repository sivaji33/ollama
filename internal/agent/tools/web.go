package tools

import (
	"errors"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ollama/ollama/envconfig"
)

const (
	webUserAgent = "ollama-agent-webknowledge/1.0"
	// webDisabledEnv explicitly disables the internet knowledge tools. They
	// talk to the open web directly and do not use Ollama cloud services, so
	// they are independent of OLLAMA_NO_CLOUD.
	webDisabledEnv = "OLLAMA_AGENT_WEB_DISABLED"
)

// webHTTPClient is shared by the internet knowledge tools. Tests may replace
// it with a client that points at local fixtures.
var webHTTPClient = &http.Client{Timeout: 20 * time.Second}

var (
	webTagRE             = regexp.MustCompile(`(?s)<[^>]*>`)
	webHorizontalSpaceRE = regexp.MustCompile(`[^\S\n]+`)
	webBlankLineRE       = regexp.MustCompile(`\n{3,}`)
)

// webAccessError reports whether internet knowledge tools were explicitly
// disabled with OLLAMA_AGENT_WEB_DISABLED. These tools make direct keyless
// requests to the open web, so an installation that prefers no outbound
// research can turn them off without affecting anything else.
func webAccessError(operation string) error {
	if disabled, err := strconv.ParseBool(strings.TrimSpace(envconfig.Var(webDisabledEnv))); err == nil && disabled {
		return errors.New(operation + " is disabled by " + webDisabledEnv)
	}
	return nil
}

// cleanWebText converts markup or raw text to readable form: HTML entities are
// decoded, tags collapse to spaces, horizontal whitespace runs are compressed,
// and runs of blank lines are reduced.
func cleanWebText(raw string) string {
	text := webTagRE.ReplaceAllString(raw, " ")
	text = html.UnescapeString(text)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(webHorizontalSpaceRE.ReplaceAllString(line, " "))
	}
	text = strings.Join(lines, "\n")
	return strings.TrimSpace(webBlankLineRE.ReplaceAllString(text, "\n\n"))
}

// truncateWebChars clamps text to limit bytes on a rune boundary and reports
// whether truncation occurred.
func truncateWebChars(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	return strings.TrimRight(strings.ToValidUTF8(text[:limit], ""), "\n"), true
}
