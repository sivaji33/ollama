package muse

import (
	"context"
	"errors"
	"sort"
	"sync"
)

type Connector interface {
	Name() string
	Capabilities() []string
	Call(context.Context, string, map[string]any) (string, error)
}

type ConnectorRegistry struct {
	mu    sync.RWMutex
	items map[string]Connector
}

func NewConnectorRegistry() *ConnectorRegistry {
	return &ConnectorRegistry{items: map[string]Connector{}}
}

func (r *ConnectorRegistry) Register(c Connector) {
	if c == nil {
		return
	}
	r.mu.Lock()
	r.items[c.Name()] = c
	r.mu.Unlock()
}

func (r *ConnectorRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.items))
	for name := range r.items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *ConnectorRegistry) Call(ctx context.Context, name, operation string, args map[string]any) (string, error) {
	r.mu.RLock()
	c := r.items[name]
	r.mu.RUnlock()
	if c == nil {
		return "", errors.New("connector is not registered: " + name)
	}
	return c.Call(ctx, operation, args)
}
