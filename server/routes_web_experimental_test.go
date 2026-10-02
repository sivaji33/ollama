package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ollama/ollama/api"
	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

const experimentalWebSearchFixture = `<html><body>
<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fdocs">Example Docs</a>
<td class="result__snippet">Readable snippet text</td>
<a class="result__a" href="https://go.dev/doc/effective_go">Effective Go</a>
<td class="result__snippet">Idiomatic Go guidance</td>
</body></html>`

func postExperimentalWeb(t *testing.T, s *Server, path, body string) (*http.Response, []byte) {
	t.Helper()
	router, err := s.GenerateRoutes()
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(router)
	t.Cleanup(local.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, local.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := local.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	payload, _ := io.ReadAll(resp.Body)
	return resp, payload
}

// TestExperimentalWebSearchUsesLocalInternetSearch proves the experimental web
// search endpoint answers from OwnBot's own keyless internet search instead of
// proxying to an Ollama service.
func TestExperimentalWebSearchUsesLocalInternetSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setTestHome(t, t.TempDir())

	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.PostFormValue("q"); got != "effective go" {
			t.Errorf("query = %q, want %q", got, "effective go")
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, experimentalWebSearchFixture)
	}))
	defer search.Close()
	t.Setenv("OLLAMA_WEB_SEARCH_URL", search.URL)

	resp, body := postExperimentalWeb(t, &Server{}, "/api/experimental/web_search", `{"query":"effective go","max_results":2}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	var got api.WebSearchResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("results = %+v", got.Results)
	}
	if got.Results[0].Title != "Example Docs" || got.Results[0].URL != "https://example.com/docs" || got.Results[0].Content != "Readable snippet text" {
		t.Fatalf("first result = %+v", got.Results[0])
	}
}

// TestExperimentalWebFetchUsesLocalDirectFetch proves the experimental web
// fetch endpoint downloads pages directly instead of using an Ollama service.
func TestExperimentalWebFetchUsesLocalDirectFetch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setTestHome(t, t.TempDir())

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head><title>Local Page</title></head><body><p>Hello internet</p><a href="/next">Next</a></body></html>`)
	}))
	defer page.Close()

	original := agenttools.WebFetchHostGuard
	agenttools.WebFetchHostGuard = func(string) error { return nil }
	t.Cleanup(func() { agenttools.WebFetchHostGuard = original })

	resp, body := postExperimentalWeb(t, &Server{}, "/api/experimental/web_fetch", `{"url":"`+page.URL+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	var got api.WebFetchResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "Local Page" {
		t.Fatalf("title = %q", got.Title)
	}
	if !strings.Contains(got.Content, "Hello internet") {
		t.Fatalf("content = %q", got.Content)
	}
	if len(got.Links) == 0 || got.Links[0] != page.URL+"/next" {
		t.Fatalf("links = %v", got.Links)
	}
}

func TestExperimentalWebEndpointsRejectBadRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setTestHome(t, t.TempDir())

	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		{name: "search malformed json", path: "/api/experimental/web_search", body: `{`, want: http.StatusBadRequest},
		{name: "search empty query", path: "/api/experimental/web_search", body: `{"query":"   "}`, want: http.StatusBadRequest},
		{name: "fetch unsupported scheme", path: "/api/experimental/web_fetch", body: `{"url":"ftp://example.com/file"}`, want: http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := postExperimentalWeb(t, &Server{}, tc.path, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d (body=%s)", resp.StatusCode, tc.want, body)
			}
		})
	}
}
