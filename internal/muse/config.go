package muse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	AllowedRoots     []string `json:"allowed_roots"`
	Workspace        string   `json:"workspace"`
	StateDir         string   `json:"state_dir"`
	DownloadDir      string   `json:"download_dir"`
	ShellTimeoutSecs int      `json:"shell_timeout_secs"`
}

func DefaultConfig(workspace string) Config {
	state := filepath.Join(workspace, ".muse")
	return Config{
		AllowedRoots:     []string{workspace},
		Workspace:        workspace,
		StateDir:         state,
		DownloadDir:      filepath.Join(state, "downloads"),
		ShellTimeoutSecs: 300,
	}
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

func BuildRuntime(c Config) (*Runtime, error) {
	guard, err := NewPathGuard(c.AllowedRoots...)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(c.ShellTimeoutSecs) * time.Second
	r := NewRuntime()
	r.Files = &FileSystem{Guard: guard}
	r.Shell = &Shell{WorkDir: c.Workspace, Timeout: timeout}
	pm := ProcessManager{}
	r.Processes = &pm
	r.Network = &Network{Guard: guard}
	r.Git = &Git{WorkDir: c.Workspace}
	r.Memory = &Memory{Path: filepath.Join(c.StateDir, "memory.jsonl")}
	r.Journal = &Journal{Path: filepath.Join(c.StateDir, "journal.jsonl")}
	r.Checkpoints = &CheckpointStore{Root: filepath.Join(c.StateDir, "checkpoints"), Guard: guard}
	return r, nil
}
