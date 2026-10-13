package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	HTTP       *http.Client
	githubBase string
	googleBase string
	serperBase string
}

func NewClient() *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		githubBase: "https://api.github.com",
		googleBase: "https://generativelanguage.googleapis.com/v1beta",
		serperBase: "https://google.serper.dev/search",
	}
}

type Repository struct {
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	HTMLURL     string `json:"html_url"`
	Stars       int    `json:"stargazers_count"`
}

type GitHubSearchResult struct {
	TotalCount int          `json:"total_count"`
	Items      []Repository `json:"items"`
}

type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type GoogleResult struct {
	Answer        string   `json:"answer"`
	Grounded      bool     `json:"grounded"`
	Sources       []Source `json:"sources"`
	SearchQueries []string `json:"search_queries,omitempty"`
}

func (c *Client) execute(req *http.Request, target any) error {
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf(
			"provider HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	return json.NewDecoder(
		io.LimitReader(resp.Body, 2<<20),
	).Decode(target)
}

func (c *Client) githubRequest(
	ctx context.Context,
	endpoint string,
	target any,
) error {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, endpoint, nil,
	)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "OwnBot-MUSE")

	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return c.execute(req, target)
}

func (c *Client) GitHubSearch(
	ctx context.Context,
	query string,
) (GitHubSearchResult, error) {
	var result GitHubSearchResult

	query = strings.TrimSpace(query)
	if query == "" || len(query) > 256 {
		return result, errors.New("GitHub query must contain 1-256 bytes")
	}

	u, err := url.Parse(c.githubBase + "/search/repositories")
	if err != nil {
		return result, err
	}

	values := url.Values{}
	values.Set("q", query)
	values.Set("per_page", "5")
	u.RawQuery = values.Encode()

	err = c.githubRequest(ctx, u.String(), &result)
	return result, err
}

var repoSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (c *Client) GitHubRepo(
	ctx context.Context,
	fullName string,
) (Repository, error) {
	var result Repository
	parts := strings.Split(fullName, "/")

	if len(parts) != 2 ||
		!repoSegment.MatchString(parts[0]) ||
		!repoSegment.MatchString(parts[1]) ||
		parts[0] == "." || parts[0] == ".." ||
		parts[1] == "." || parts[1] == ".." {
		return result, errors.New("expected GitHub owner/repository")
	}

	endpoint := c.githubBase + "/repos/" +
		url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])

	err := c.githubRequest(ctx, endpoint, &result)
	return result, err
}

func (c *Client) GoogleSearch(
	ctx context.Context,
	query string,
) (GoogleResult, error) {
	var result GoogleResult

	query = strings.TrimSpace(query)
	if query == "" || len(query) > 2000 {
		return result, errors.New("invalid Google search query")
	}

	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		return result, errors.New("GEMINI_API_KEY is not configured")
	}

	payload := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]string{"text": query},
				},
			},
		},
		"tools": []any{
			map[string]any{"google_search": map[string]any{}},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}

	endpoint := c.googleBase +
		"/models/gemini-2.5-flash:generateContent"

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return result, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", key)

	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			GroundingMetadata struct {
				WebSearchQueries []string `json:"webSearchQueries"`
				GroundingChunks  []struct {
					Web struct {
						URI   string `json:"uri"`
						Title string `json:"title"`
					} `json:"web"`
				} `json:"groundingChunks"`
			} `json:"groundingMetadata"`
		} `json:"candidates"`
	}

	if err := c.execute(req, &response); err != nil {
		return result, err
	}

	if len(response.Candidates) == 0 {
		return result, errors.New("Google returned no answer")
	}

	candidate := response.Candidates[0]
	var answer []string

	for _, part := range candidate.Content.Parts {
		if strings.TrimSpace(part.Text) != "" {
			answer = append(answer, part.Text)
		}
	}

	result.Answer = strings.Join(answer, "\n")
	result.SearchQueries = candidate.GroundingMetadata.WebSearchQueries

	seen := make(map[string]bool)

	for _, chunk := range candidate.GroundingMetadata.GroundingChunks {
		u := chunk.Web.URI
		parsed, err := url.Parse(u)

		if err != nil || parsed.Host == "" ||
			(parsed.Scheme != "https" && parsed.Scheme != "http") ||
			seen[u] {
			continue
		}

		seen[u] = true
		result.Sources = append(result.Sources, Source{
			Title: chunk.Web.Title,
			URL:   u,
		})
	}

	result.Grounded = len(result.Sources) > 0

	if strings.TrimSpace(result.Answer) == "" {
		return result, errors.New("Google returned no text")
	}

	return result, nil
}
