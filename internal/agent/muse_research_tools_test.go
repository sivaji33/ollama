package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/internal/muse/integrations"
)

type museResearchTransport func(*http.Request) (*http.Response, error)

func (f museResearchTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return f(request)
}

func TestMUSEResearchToolsRegistered(t *testing.T) {
	names := []string{
		"google_search",
		"github_search",
		"github_repo",
	}

	for _, name := range names {
		found := false

		for _, item := range agentTools() {
			if item.Function.Name == name {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("AI tool not registered: %s", name)
		}

		if MuseToolNeedsApproval(name) {
			t.Errorf("read-only tool unexpectedly requires approval: %s", name)
		}
	}
}

func TestMUSEResearchToolsExecute(t *testing.T) {
	t.Setenv("SERPER_API_KEY", "test-serper-key")
	t.Setenv("GITHUB_TOKEN", "")

	client := integrations.NewClient()

	client.HTTP = &http.Client{
		Timeout: 3 * time.Second,
		Transport: museResearchTransport(
			func(r *http.Request) (*http.Response, error) {
				var body string

				switch {
				case r.URL.Host == "google.serper.dev" &&
					r.URL.Path == "/search":
					if r.Method != http.MethodPost ||
						r.Header.Get("X-API-KEY") != "test-serper-key" {
						t.Error("unexpected Serper request")
					}
					body = `{"organic":[{"title":"Angular docs","link":"https://angular.dev","snippet":"Angular documentation","position":1}]}`

				case r.URL.Host == "api.github.com" &&
					r.URL.Path == "/search/repositories":
					if r.Method != http.MethodGet {
						t.Error("GitHub search must be read-only GET")
					}
					body = `{"total_count":1,"items":[{"full_name":"angular/angular","html_url":"https://github.com/angular/angular","stargazers_count":10}]}`

				case r.URL.Host == "api.github.com" &&
					r.URL.Path == "/repos/angular/angular":
					if r.Method != http.MethodGet {
						t.Error("GitHub repo must be read-only GET")
					}
					body = `{"full_name":"angular/angular","html_url":"https://github.com/angular/angular","stargazers_count":10}`

				default:
					t.Errorf("unexpected network endpoint: %s", r.URL)
					body = `{}`
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			},
		),
	}

	cases := []struct {
		name     string
		query    string
		expected string
	}{
		{"google_search", "Angular docs", "Angular docs"},
		{"github_search", "angular", "angular/angular"},
		{"github_repo", "angular/angular", "angular/angular"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := api.NewToolCallFunctionArguments()
			args.Set("query", tc.query)

			call := api.ToolCall{
				Function: api.ToolCallFunction{
					Name:      tc.name,
					Arguments: args,
				},
			}

			result, err := executeMUSEResearchToolWithClient(
				context.Background(),
				call,
				client,
			)
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(result, tc.expected) {
				t.Fatalf("unexpected tool output: %s", result)
			}
		})
	}
}

func TestMUSEResearchMissingArgument(t *testing.T) {
	call := api.ToolCall{
		Function: api.ToolCallFunction{
			Name: "google_search",
		},
	}

	_, err := executeMUSEResearchToolWithClient(
		context.Background(),
		call,
		integrations.NewClient(),
	)
	if err == nil {
		t.Fatal("missing query should be rejected")
	}
}
