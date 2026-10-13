package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/internal/muse/integrations"
)

func executeMUSEResearchTool(
	ctx context.Context,
	call api.ToolCall,
) (string, error) {
	return executeMUSEResearchToolWithClient(
		ctx,
		call,
		integrations.NewClient(),
	)
}

func executeMUSEResearchToolWithClient(
	ctx context.Context,
	call api.ToolCall,
	client *integrations.Client,
) (string, error) {
	if client == nil {
		return "", errors.New("research client is missing")
	}

	query, err := stringArg(&call.Function.Arguments, "query")
	if err != nil {
		return "", err
	}

	var data any

	switch call.Function.Name {
	case "google_search":
		data, err = client.SerperSearch(ctx, query)
	case "github_search":
		data, err = client.GitHubSearch(ctx, query)
	case "github_repo":
		data, err = client.GitHubRepo(ctx, query)
	default:
		return "", fmt.Errorf(
			"unknown research tool %q",
			call.Function.Name,
		)
	}

	if err != nil {
		return "", fmt.Errorf(
			"%s failed: %w",
			call.Function.Name,
			err,
		)
	}

	output, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}

	return string(output), nil
}
