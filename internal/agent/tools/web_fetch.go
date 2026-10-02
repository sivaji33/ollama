package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	maxWebFetchBytes = 1024 * 1024
	maxWebTextBytes  = 16 * 1024
)

var (
	webBoilerplateRE = regexp.MustCompile(`(?is)<(script|style|noscript|template|svg|head)[^>]*>.*?</(script|style|noscript|template|svg|head)>`)
	webTitleRE       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	webLinkRE        = regexp.MustCompile(`(?is)<a\s[^>]*href\s*=\s*"([^"]*)"`)
)

// webFetchHostGuard blocks obviously internal destinations such as loopback,
// private, and link-local addresses. It is a lexical host guard, not an
// operating-system firewall: a public hostname that resolves to a private
// address is not detected. Tests may replace it to reach local fixtures.
var webFetchHostGuard = rejectPrivateWebHosts

// WebFetchHostGuard exposes the host guard so other local OwnBot surfaces
// (and their tests) can reason about or substitute it. It is never nil in
// production; a nil guard means "allow everything".
var WebFetchHostGuard = func(host string) error {
	if webFetchHostGuard == nil {
		return nil
	}
	return webFetchHostGuard(host)
}

// WebPage is the bounded, readable form of a fetched internet page.
type WebPage struct {
	URL         string   `json:"url"`
	Title       string   `json:"title,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	Links       []string `json:"links,omitempty"`
	Text        string   `json:"text"`
}

// WebFetch downloads an absolute http(s) URL and returns its readable text so
// the agent can learn from documentation, standards, and examples discovered
// with web_search. Redirects are followed by the shared client, and the
// response body is bounded before conversion to text.
func WebFetch(ctx context.Context, rawURL string) (string, error) {
	page, err := FetchWebPage(ctx, rawURL)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(page)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// FetchWebPage downloads an absolute http(s) URL and returns its bounded,
// readable form as structured data. It is the shared implementation used by
// the agent tools and by other local OwnBot surfaces so none of them depend on
// an Ollama service.
func FetchWebPage(ctx context.Context, rawURL string) (*WebPage, error) {
	if err := webAccessError("web fetch"); err != nil {
		return nil, err
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("url scheme %q is not supported; use http or https", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("url host is required")
	}
	if err := WebFetchHostGuard(parsed.Hostname()); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build web fetch request: %w", err)
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "text/html, text/plain, application/json, application/xml;q=0.9, */*;q=0.5")
	resp, err := webHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web fetch request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("web fetch returned status %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if !supportedWebContentType(contentType) {
		return nil, fmt.Errorf("web fetch content type %q is not readable text", contentType)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read web fetch response: %w", err)
	}
	truncated := len(body) > maxWebFetchBytes
	if truncated {
		body = body[:maxWebFetchBytes]
	}
	raw := string(body)
	page := &WebPage{
		URL:         parsed.String(),
		ContentType: mediaType(contentType),
		Truncated:   truncated,
	}
	if isHTMLContentType(contentType) {
		if match := webTitleRE.FindStringSubmatch(raw); match != nil {
			page.Title, _ = truncateWebChars(cleanWebText(match[1]), maxWebTitleChars)
		}
		page.Links = extractWebLinks(raw, parsed)
		raw = webBoilerplateRE.ReplaceAllString(raw, "\n")
	}
	text, textTruncated := truncateWebChars(cleanWebText(raw), maxWebTextBytes)
	page.Text = text
	page.Truncated = page.Truncated || textTruncated
	return page, nil
}

// extractWebLinks pulls bounded, absolute http(s) links from raw HTML so
// callers can offer follow-up reading targets.
func extractWebLinks(rawHTML string, base *url.URL) []string {
	const maxLinks = 50
	matches := webLinkRE.FindAllStringSubmatch(rawHTML, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	links := make([]string, 0, min(len(matches), maxLinks))
	for _, match := range matches {
		if len(links) >= maxLinks {
			break
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		if href == "" || strings.HasPrefix(href, "#") {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref)
		if abs.Scheme != "http" && abs.Scheme != "https" {
			continue
		}
		value := abs.String()
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		links = append(links, value)
	}
	return links
}

func mediaType(contentType string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
}

func isHTMLContentType(contentType string) bool {
	return strings.Contains(mediaType(contentType), "html")
}

func supportedWebContentType(contentType string) bool {
	kind := mediaType(contentType)
	if kind == "" || strings.HasPrefix(kind, "text/") {
		return true
	}
	switch kind {
	case "application/json", "application/xml", "application/xhtml+xml", "application/x-ndjson":
		return true
	}
	return false
}

// rejectPrivateWebHosts is the default guard for web_fetch destinations.
func rejectPrivateWebHosts(host string) error {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if name == "" {
		return errors.New("url host is required")
	}
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return fmt.Errorf("web fetch host %q is blocked", host)
	}
	if ip := net.ParseIP(name); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("web fetch host %q is blocked", host)
		}
	}
	return nil
}
