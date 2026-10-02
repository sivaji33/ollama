//go:build windows || darwin

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

type WebSearch struct{}

type SearchRequest struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

func (w *WebSearch) Name() string {
	return "web_search"
}

func (w *WebSearch) Description() string {
	return "Search the web for real-time information using the built-in keyless internet search."
}

func (w *WebSearch) Prompt() string {
	return ""
}

func (g *WebSearch) Schema() map[string]any {
	schemaBytes := []byte(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "The search query to execute"
			},
			"max_results": {
				"type": "integer",
				"description": "Maximum number of search results to return",
				"default": 3
			}
		},
		"required": ["query"]
	}`)
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil
	}
	return schema
}

func (w *WebSearch) Execute(ctx context.Context, args map[string]any) (any, string, error) {
	rawQuery, ok := args["query"]
	if !ok {
		return nil, "", fmt.Errorf("query parameter is required")
	}

	queryStr, ok := rawQuery.(string)
	if !ok || strings.TrimSpace(queryStr) == "" {
		return nil, "", fmt.Errorf("query must be a non-empty string")
	}

	maxResults := 5
	if v, ok := args["max_results"].(float64); ok && int(v) > 0 {
		maxResults = int(v)
	}

	result, err := performWebSearch(ctx, queryStr, maxResults)
	if err != nil {
		return nil, "", err
	}
	for _, result := range result.Results {
		addAllowedDirectURL(ctx, result.URL)
	}

	return result, "", nil
}

// performWebSearch runs the shared keyless internet search. OwnBot talks to
// the open web directly, so this tool does not require any Ollama service.
func performWebSearch(ctx context.Context, query string, maxResults int) (*SearchResponse, error) {
	results, err := agenttools.SearchWeb(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}

	response := &SearchResponse{Results: make([]SearchResult, 0, len(results))}
	for _, item := range results {
		response.Results = append(response.Results, SearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Content: item.Snippet,
		})
	}
	return response, nil
}
