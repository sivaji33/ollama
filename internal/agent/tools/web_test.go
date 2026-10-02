package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const webSearchFixtureTemplate = `<html><body><div class="result results_links">
<h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%%3A%%2F%%2Fexample.com%%2Fdoc%%2F%d&amp;rut=abc">Example <b>Doc</b> %d</a></h2>
<a class="result__snippet" href="https://example.com/doc/%d">Snippet <i>%d</i> for the docs.</a>
</div></body></html>`

func webSearchFixture(count int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, webSearchFixtureTemplate, i, i, i, i)
	}
	b.WriteString("</body></html>")
	return b.String()
}

func allowLocalFixtureHosts(t *testing.T) {
	t.Helper()
	previous := webFetchHostGuard
	webFetchHostGuard = func(string) error { return nil }
	t.Cleanup(func() { webFetchHostGuard = previous })
}

func TestWebSearchParsesResultsAndDecodesLinks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.PostFormValue("q"); got != "effective go" {
			t.Errorf("query = %q, want %q", got, "effective go")
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, webSearchFixture(3))
	}))
	defer server.Close()
	t.Setenv(webSearchEndpointEnv, server.URL)

	out, err := WebSearch(context.Background(), "effective go", 0)
	if err != nil {
		t.Fatal(err)
	}
	var resp WebSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("results = %d, want 3 (default count)", len(resp.Results))
	}
	first := resp.Results[0]
	if first.URL != "https://example.com/doc/0" {
		t.Fatalf("decoded url = %q, want redirect target", first.URL)
	}
	if first.Title != "Example Doc 0" {
		t.Fatalf("title = %q, want tag-free text", first.Title)
	}
	if first.Snippet != "Snippet 0 for the docs." {
		t.Fatalf("snippet = %q", first.Snippet)
	}
}

func TestWebSearchClampsResultCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, webSearchFixture(12))
	}))
	defer server.Close()
	t.Setenv(webSearchEndpointEnv, server.URL)

	parse := func(raw string) WebSearchResponse {
		t.Helper()
		var resp WebSearchResponse
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	defaultOut, err := WebSearch(context.Background(), "golang", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(parse(defaultOut).Results); got != 5 {
		t.Fatalf("default results = %d, want 5", got)
	}

	cappedOut, err := WebSearch(context.Background(), "golang", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(parse(cappedOut).Results); got != 10 {
		t.Fatalf("capped results = %d, want 10", got)
	}
}

func TestWebSearchRejectsEmptyQueryAndErrors(t *testing.T) {
	if _, err := WebSearch(context.Background(), "   ", 1); err == nil {
		t.Fatal("expected empty query rejection")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv(webSearchEndpointEnv, server.URL)
	if _, err := WebSearch(context.Background(), "golang", 1); err == nil {
		t.Fatal("expected search error on non-2xx status")
	}
}

func TestWebFetchExtractsReadableText(t *testing.T) {
	page := `<html><head><title>Docs Page</title><style>body{color:red}</style><script>var secret="hidden";</script></head><body><h1>Hello &amp; welcome</h1><p>First   paragraph.</p></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("expected a user agent header")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	}))
	defer server.Close()
	allowLocalFixtureHosts(t)

	out, err := WebFetch(context.Background(), server.URL+"/docs")
	if err != nil {
		t.Fatal(err)
	}
	var fetched WebPage
	if err := json.Unmarshal([]byte(out), &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Title != "Docs Page" {
		t.Fatalf("title = %q", fetched.Title)
	}
	if !strings.Contains(fetched.Text, "Hello & welcome") {
		t.Fatalf("text missing decoded entity: %q", fetched.Text)
	}
	if !strings.Contains(fetched.Text, "First paragraph.") {
		t.Fatalf("text missing collapsed whitespace: %q", fetched.Text)
	}
	if strings.Contains(fetched.Text, "hidden") || strings.Contains(fetched.Text, "color:red") {
		t.Fatalf("script or style leaked into text: %q", fetched.Text)
	}
	if fetched.Truncated {
		t.Fatalf("small page marked truncated: %+v", fetched)
	}
}

func TestWebFetchRejectsUnsupportedURLs(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com/x", "file:///tmp/x", "example.com/no-scheme"} {
		if _, err := WebFetch(context.Background(), raw); err == nil {
			t.Fatalf("%q: expected rejection", raw)
		}
	}
}

func TestWebFetchHostGuardBlocksInternalTargets(t *testing.T) {
	for _, raw := range []string{
		"http://localhost/x",
		"http://127.0.0.1:8080/x",
		"http://10.1.2.3/x",
		"http://172.16.5.5/x",
		"http://192.168.1.10/x",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/x",
	} {
		if _, err := WebFetch(context.Background(), raw); err == nil {
			t.Fatalf("%q: expected blocked host", raw)
		}
	}
}

func TestWebFetchRejectsErrorStatusAndBinaryContent(t *testing.T) {
	allowLocalFixtureHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/error":
			http.Error(w, "missing", http.StatusNotFound)
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "\x00\x01binary")
		}
	}))
	defer server.Close()

	if _, err := WebFetch(context.Background(), server.URL+"/error"); err == nil {
		t.Fatal("expected status error")
	}
	if _, err := WebFetch(context.Background(), server.URL+"/binary"); err == nil {
		t.Fatal("expected content type error")
	}
}

func TestWebFetchTruncatesOversizedPages(t *testing.T) {
	allowLocalFixtureHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, strings.Repeat("a", maxWebFetchBytes+2048))
	}))
	defer server.Close()

	out, err := WebFetch(context.Background(), server.URL+"/big")
	if err != nil {
		t.Fatal(err)
	}
	var fetched WebPage
	if err := json.Unmarshal([]byte(out), &fetched); err != nil {
		t.Fatal(err)
	}
	if !fetched.Truncated {
		t.Fatal("expected oversized page to be marked truncated")
	}
	if len(fetched.Text) > maxWebTextBytes {
		t.Fatalf("text length = %d, want <= %d", len(fetched.Text), maxWebTextBytes)
	}
}

func TestWebToolsHonorDisableSwitch(t *testing.T) {
	t.Setenv(webDisabledEnv, "1")
	if _, err := WebSearch(context.Background(), "golang", 1); err == nil {
		t.Fatal("expected web search disabled error")
	}
	if _, err := WebFetch(context.Background(), "https://example.com"); err == nil {
		t.Fatal("expected web fetch disabled error")
	}
}
