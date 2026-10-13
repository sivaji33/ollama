package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/search/repositories" ||
				r.URL.Query().Get("q") != "angular" {
				t.Errorf("unexpected request: %s", r.URL)
			}
			fmt.Fprint(w, `{"total_count":1,"items":[{"full_name":"example/angular","html_url":"https://github.com/example/angular","stargazers_count":10}]}`)
		},
	))
	defer server.Close()

	client := NewClient()
	client.githubBase = server.URL

	result, err := client.GitHubSearch(context.Background(), "angular")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 ||
		result.Items[0].FullName != "example/angular" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGitHubRejectsInvalidRepo(t *testing.T) {
	_, err := NewClient().GitHubRepo(
		context.Background(), "../secret",
	)
	if err == nil {
		t.Fatal("expected invalid repository to be rejected")
	}
}

func TestGoogleSearch(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-goog-api-key") != "test-key" {
				t.Error("missing API authentication")
			}
			fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"Verified answer"}]},"groundingMetadata":{"groundingChunks":[{"web":{"uri":"https://example.com/source","title":"Source"}}]}}]}`)
		},
	))
	defer server.Close()

	client := NewClient()
	client.googleBase = server.URL

	result, err := client.GoogleSearch(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Grounded || result.Answer != "Verified answer" ||
		len(result.Sources) != 1 {
		t.Fatalf("unexpected response: %+v", result)
	}
}

func TestGoogleRequiresAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	_, err := NewClient().GoogleSearch(
		context.Background(), "test",
	)
	if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("expected missing-key error, got %v", err)
	}
}
