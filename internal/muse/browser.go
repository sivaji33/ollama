package muse

import (
	"context"
	"errors"
)

type BrowserDriver interface {
	Open(context.Context, string) (string, error)
	Click(context.Context, string) (string, error)
	Type(context.Context, string, string) (string, error)
	Read(context.Context) (string, error)
	Screenshot(context.Context) ([]byte, error)
}

type Browser struct {
	Driver BrowserDriver
}

func (b *Browser) Action(ctx context.Context, operation string, args map[string]any) (string, error) {
	if b == nil || b.Driver == nil {
		return "", errors.New("browser driver is not connected")
	}
	switch operation {
	case "open":
		url, _ := args["url"].(string)
		return b.Driver.Open(ctx, url)
	case "click":
		selector, _ := args["selector"].(string)
		return b.Driver.Click(ctx, selector)
	case "type":
		selector, _ := args["selector"].(string)
		text, _ := args["text"].(string)
		return b.Driver.Type(ctx, selector, text)
	case "read":
		return b.Driver.Read(ctx)
	default:
		return "", errors.New("unsupported browser operation")
	}
}
