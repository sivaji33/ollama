//go:build windows || darwin

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttools "github.com/ollama/ollama/internal/agent/tools"
)

type WebFetch struct{}

type FetchRequest struct {
	URL string `json:"url"`
}

type FetchResponse struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Links   []string `json:"links"`
}

func (w *WebFetch) Name() string {
	return "web_fetch"
}

func (w *WebFetch) Description() string {
	return "Crawl and extract text content from web pages"
}

func (g *WebFetch) Schema() map[string]any {
	schemaBytes := []byte(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "URL to crawl and extract content from"
            }
		},
		"required": ["url"]
	}`)
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil
	}
	return schema
}

func (w *WebFetch) Prompt() string {
	return ""
}

func (w *WebFetch) Execute(ctx context.Context, args map[string]any) (any, string, error) {
	urlRaw, ok := args["url"]
	if !ok {
		return nil, "", fmt.Errorf("url parameter is required")
	}
	urlStr, ok := urlRaw.(string)
	if !ok || strings.TrimSpace(urlStr) == "" {
		return nil, "", fmt.Errorf("url must be a non-empty string")
	}
	if !allowedDirectURL(ctx, urlStr) {
		return nil, "", fmt.Errorf("web fetch is only allowed for URLs provided by the user")
	}

	result, err := performWebFetch(ctx, urlStr)
	if err != nil {
		return nil, "", err
	}
	for _, link := range result.Links {
		addAllowedDirectURL(ctx, link)
	}

	return result, "", nil
}

// performWebFetch downloads the page directly from the open web. OwnBot talks
// to web pages itself, so this tool does not require any Ollama service.
func performWebFetch(ctx context.Context, targetURL string) (*FetchResponse, error) {
	page, err := agenttools.FetchWebPage(ctx, targetURL)
	if err != nil {
		return nil, err
	}
	return &FetchResponse{Title: page.Title, Content: page.Text, Links: page.Links}, nil
}
