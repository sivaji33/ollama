package muse

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Journal struct {
	Path string
	mu   sync.Mutex
}

func (j *Journal) Append(event Event) error {
	if j == nil || j.Path == "" {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(j.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(event)
}

func (j *Journal) Events(sessionID string) ([]Event, error) {
	if j == nil || j.Path == "" {
		return nil, nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := os.Open(j.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Event
	s := bufio.NewScanner(f)
	for s.Scan() {
		var e Event
		if json.Unmarshal(s.Bytes(), &e) == nil && (sessionID == "" || e.SessionID == sessionID) {
			out = append(out, e)
		}
	}
	return out, s.Err()
}
