package muse

import (
	"context"
	"errors"
	"sort"
	"sync"
)

type ToolFunc func(context.Context, Request) (string, error)

type ToolSpec struct {
	Action      Action `json:"action"`
	Description string `json:"description"`
	Risk        Risk   `json:"risk"`
}

type Registry struct {
	mu    sync.RWMutex
	specs map[Action]ToolSpec
	tools map[Action]ToolFunc
}

func NewRegistry() *Registry {
	return &Registry{specs: map[Action]ToolSpec{}, tools: map[Action]ToolFunc{}}
}

func (r *Registry) Register(spec ToolSpec, fn ToolFunc) {
	r.mu.Lock()
	r.specs[spec.Action] = spec
	r.tools[spec.Action] = fn
	r.mu.Unlock()
}

func (r *Registry) Specs() []ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolSpec, 0, len(r.specs))
	for _, s := range r.specs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return out
}

func (r *Registry) Execute(ctx context.Context, req Request) (string, error) {
	r.mu.RLock()
	fn := r.tools[req.Action]
	r.mu.RUnlock()
	if fn == nil {
		return "", errors.New("Muse tool not registered: " + string(req.Action))
	}
	return fn(ctx, req)
}
