package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
)

type SerperItem struct {
	Title    string `json:"title"`
	Link     string `json:"link"`
	Snippet  string `json:"snippet"`
	Position int    `json:"position"`
}

type SerperSearchResult struct {
	Organic []SerperItem `json:"organic"`
}

func (c *Client) SerperSearch(
	ctx context.Context,
	query string,
) (SerperSearchResult, error) {
	var result SerperSearchResult

	query = strings.TrimSpace(query)
	if query == "" || len(query) > 2000 {
		return result, errors.New("invalid search query")
	}

	key := strings.TrimSpace(os.Getenv("SERPER_API_KEY"))
	if key == "" {
		return result, errors.New("SERPER_API_KEY not configured")
	}

	body, err := json.Marshal(map[string]any{
		"q":   query,
		"num": 5,
	})
	if err != nil {
		return result, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.serperBase,
		bytes.NewReader(body),
	)
	if err != nil {
		return result, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", key)

	err = c.execute(req, &result)
	return result, err
}
