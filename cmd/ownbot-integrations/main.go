package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ollama/ollama/internal/muse/integrations"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr,
			"Usage: ownbot-integrations <github-search|github-repo|google> <query>")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	client := integrations.NewClient()
	input := strings.Join(os.Args[2:], " ")

	var result any
	var err error

	switch os.Args[1] {
	case "github-search":
		result, err = client.GitHubSearch(ctx, input)
	case "github-repo":
		result, err = client.GitHubRepo(ctx, input)
	case "google":
		result, err = client.GoogleSearch(ctx, input)
	case "google-serper":
		result, err = client.SerperSearch(ctx, input)
	default:
		fmt.Fprintln(os.Stderr, "Unknown command")
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "Integration error:", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
