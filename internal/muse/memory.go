package muse

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryRecord struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Source    string    `json:"source,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Memory struct {
	Path string
	mu   sync.Mutex
}

func (m *Memory) Remember(key, value, source string, tags ...string) (MemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := MemoryRecord{
		ID: newID("memory"), Key: key, Value: value, Source: source,
		Tags: tags, CreatedAt: time.Now().UTC(),
	}
	if err := os.MkdirAll(filepath.Dir(m.Path), 0o700); err != nil {
		return rec, err
	}
	f, err := os.OpenFile(m.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return rec, err
	}
	defer f.Close()
	return rec, json.NewEncoder(f).Encode(rec)
}

func (m *Memory) Recall(query string, limit int) ([]MemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := os.Open(m.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	q := strings.ToLower(strings.TrimSpace(query))
	var out []MemoryRecord
	s := bufio.NewScanner(f)
	for s.Scan() {
		var rec MemoryRecord
		if json.Unmarshal(s.Bytes(), &rec) != nil {
			continue
		}
		hay := strings.ToLower(rec.Key + " " + rec.Value + " " + rec.Source + " " + strings.Join(rec.Tags, " "))
		if q == "" || strings.Contains(hay, q) {
			out = append(out, rec)
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
