package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/ollama/ollama/envconfig"
)

const (
	defaultWebSearchResults = 5
	maxWebSearchResults     = 10
	maxWebSearchBytes       = 512 * 1024
	maxWebSnippetChars      = 480
	maxWebTitleChars        = 200
)

const (
	// defaultWebSearchEndpoint is a keyless DuckDuckGo HTML endpoint so local
	// installations can gather internet knowledge without an API key or
	// account. OLLAMA_WEB_SEARCH_URL (or the legacy OLLAMA_AGENT_WEB_SEARCH_URL)
	// may point this at any DuckDuckGo-style HTML endpoint, for example a
	// self-hosted SearXNG instance.
	defaultWebSearchEndpoint = "https://html.duckduckgo.com/html/"
	webSearchEndpointEnv     = "OLLAMA_AGENT_WEB_SEARCH_URL"
	// genericWebSearchEndpointEnv is the shared name used by non-agent
	// callers; it takes precedence when both are set.
	genericWebSearchEndpointEnv = "OLLAMA_WEB_SEARCH_URL"
)

var (
	webSearchResultLinkRE    = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	webSearchResultSnippetRE = regexp.MustCompile(`(?is)<(?:a|div|td)[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</(?:a|div|td)>`)
)

// WebSearchResult is one citation-ready search result.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// WebSearchResponse is the bounded observation returned to the agent.
type WebSearchResponse struct {
	Query   string            `json:"query"`
	Results []WebSearchResult `json:"results"`
}

// WebSearch gathers current internet knowledge for the agent. Results are
// bounded in count and snippet length; the agent is expected to web_fetch the
// most promising URLs for detail.
func WebSearch(ctx context.Context, query string, maxResults int) (string, error) {
	results, err := SearchWeb(ctx, query, maxResults)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(WebSearchResponse{Query: strings.TrimSpace(query), Results: results})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// SearchWeb runs a keyless internet search and returns structured,
// citation-ready results. It is the shared implementation used by the agent
// tools and by other local OwnBot surfaces (the desktop app tools, the
// experimental server endpoints, and Anthropic/Responses web search), so none
// of them depend on an Ollama service.
func SearchWeb(ctx context.Context, query string, maxResults int) ([]WebSearchResult, error) {
	if err := webAccessError("web search"); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	maxResults = clampWebSearchResults(maxResults)
	endpoint := strings.TrimSpace(envconfig.Var(genericWebSearchEndpointEnv))
	if endpoint == "" {
		endpoint = strings.TrimSpace(envconfig.Var(webSearchEndpointEnv))
	}
	if endpoint == "" {
		endpoint = defaultWebSearchEndpoint
	}
	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build web search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", webUserAgent)
	resp, err := webHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web search request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("web search returned status %d", resp.StatusCode)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxWebSearchBytes))
	if err != nil {
		return nil, fmt.Errorf("read web search response: %w", err)
	}
	results := parseWebSearchResults(string(page), maxResults)
	if len(results) == 0 {
		return nil, errors.New("web search returned no parseable results")
	}
	return results, nil
}

func clampWebSearchResults(maxResults int) int {
	if maxResults <= 0 {
		return defaultWebSearchResults
	}
	if maxResults > maxWebSearchResults {
		return maxWebSearchResults
	}
	return maxResults
}

// parseWebSearchResults pairs result links with snippets by position. Titles
// and snippets are reduced to plain text and redirect links are resolved to
// the real destination so the model can cite and fetch them directly.
func parseWebSearchResults(page string, maxResults int) []WebSearchResult {
	links := webSearchResultLinkRE.FindAllStringSubmatch(page, -1)
	snippets := webSearchResultSnippetRE.FindAllStringSubmatch(page, -1)
	results := make([]WebSearchResult, 0, min(len(links), maxResults))
	for i, link := range links {
		if len(results) >= maxResults {
			break
		}
		target := decodeWebSearchHref(link[1])
		if target == "" {
			continue
		}
		title, _ := truncateWebChars(cleanWebText(link[2]), maxWebTitleChars)
		result := WebSearchResult{Title: title, URL: target}
		if i < len(snippets) {
			snippet, _ := truncateWebChars(cleanWebText(snippets[i][1]), maxWebSnippetChars)
			result.Snippet = snippet
		}
		results = append(results, result)
	}
	return results
}

// decodeWebSearchHref returns the real destination behind a search result
// link, unwrapping DuckDuckGo redirect URLs such as
// //duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com.
func decodeWebSearchHref(href string) string {
	href = html.UnescapeString(strings.TrimSpace(href))
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return href
	}
	if host := strings.ToLower(parsed.Hostname()); strings.HasSuffix(host, "duckduckgo.com") {
		if target := strings.TrimSpace(parsed.Query().Get("uddg")); target != "" {
			return target
		}
	}
	return href
}
