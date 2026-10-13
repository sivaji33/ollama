package muse

import (
	"context"
	"errors"
	"os/exec"
)

type Verification struct {
	Command string `json:"command"`
	Passed  bool   `json:"passed"`
	Output  string `json:"output,omitempty"`
}

type Verifier struct {
	WorkDir string
}

func (v *Verifier) Run(ctx context.Context, commands []string) ([]Verification, error) {
	var out []Verification
	for _, command := range commands {
		cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
		cmd.Dir = v.WorkDir
		b, err := cmd.CombinedOutput()
		item := Verification{Command: command, Passed: err == nil, Output: string(b)}
		out = append(out, item)
		if err != nil {
			return out, errors.New("verification failed: " + command)
		}
	}
	return out, nil
}
