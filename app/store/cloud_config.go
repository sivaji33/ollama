//go:build windows || darwin

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ollama/ollama/envconfig"
)

const serverConfigFilename = "server.json"

type serverConfig struct {
	// DisableOllamaCloud is a pointer so an explicit false (cloud opted in) can
	// be told apart from the field being absent (fork default: local-only).
	DisableOllamaCloud *bool `json:"disable_ollama_cloud,omitempty"`
}

// CloudDisabled returns whether cloud features should be disabled.
// The source of truth is: OLLAMA_NO_CLOUD OR ~/.ollama/server.json:disable_ollama_cloud.
func (s *Store) CloudDisabled() (bool, error) {
	disabled, _, err := s.CloudStatus()
	return disabled, err
}

// CloudStatus returns whether cloud is disabled and the source of that decision.
// Source is one of: "default", "none", "env", "config", or "both".
//
// Like the server, the desktop app is local-only by default: when neither
// OLLAMA_NO_CLOUD nor ~/.ollama/server.json sets a value, cloud stays off so
// the app does not connect to ollama.com on its own.
func (s *Store) CloudStatus() (bool, string, error) {
	if err := s.ensureDB(); err != nil {
		return false, "", err
	}

	configDisabled, configSet, err := readServerConfigCloudDisabled()
	if err != nil {
		return false, "", err
	}

	envSet := envconfig.Var("OLLAMA_NO_CLOUD") != ""
	envDisabled := envconfig.NoCloudEnv()

	switch {
	case envDisabled:
		if configSet && configDisabled {
			return true, "both", nil
		}
		return true, "env", nil
	case configSet && configDisabled:
		return true, "config", nil
	case envSet || configSet:
		return false, "none", nil
	default:
		return true, "default", nil
	}
}

// SetCloudEnabled writes the cloud setting to ~/.ollama/server.json.
func (s *Store) SetCloudEnabled(enabled bool) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	return setCloudEnabled(enabled)
}

func setCloudEnabled(enabled bool) error {
	configPath, err := serverConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create server config directory: %w", err)
	}

	configMap := map[string]any{}
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, &configMap); err != nil {
			// If the existing file is invalid JSON, overwrite with a fresh object.
			configMap = map[string]any{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read server config: %w", err)
	}

	configMap["disable_ollama_cloud"] = !enabled

	data, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal server config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("write server config: %w", err)
	}

	return nil
}

func readServerConfigCloudDisabled() (disabled bool, set bool, err error) {
	configPath, err := serverConfigPath()
	if err != nil {
		return false, false, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("read server config: %w", err)
	}

	var cfg serverConfig
	// Invalid or unexpected JSON should not block startup; treat the field as
	// unset so the local-only default applies.
	if json.Unmarshal(data, &cfg) != nil || cfg.DisableOllamaCloud == nil {
		return false, false, nil
	}
	return *cfg.DisableOllamaCloud, true, nil
}

func serverConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".ollama", serverConfigFilename), nil
}
