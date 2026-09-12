package server

import (
	"context"
	"sync"
)

type agentSessionControl struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func (c *agentSessionControl) register(
	sessionID string,
	cancel context.CancelFunc,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancels == nil {
		c.cancels = make(map[string]context.CancelFunc)
	}

	if _, exists := c.cancels[sessionID]; exists {
		return false
	}

	c.cancels[sessionID] = cancel
	return true
}
