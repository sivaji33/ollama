package middleware

import (
	"testing"

	"github.com/ollama/ollama/envconfig"
)

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	envconfig.ReloadServerConfig()
}

// enableCloudForTest sets HOME to a clean temp dir and explicitly enables
// cloud for the duration of the test. This fork defaults to local-only, so
// tests that cover cloud behavior must opt in with OLLAMA_NO_CLOUD=0.
func enableCloudForTest(t *testing.T) {
	t.Helper()
	t.Setenv("OLLAMA_NO_CLOUD", "0")
	setTestHome(t, t.TempDir())
}
