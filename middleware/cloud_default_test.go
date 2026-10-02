package middleware

import (
	"os"
	"testing"
)

// TestMain opts this package's tests into cloud so they exercise the
// cloud-enabled behavior they were written for. The fork defaults to
// local-only (an unset OLLAMA_NO_CLOUD means disabled); tests that cover the
// disabled behavior set OLLAMA_NO_CLOUD=1 themselves.
func TestMain(m *testing.M) {
	os.Setenv("OLLAMA_NO_CLOUD", "0")
	os.Exit(m.Run())
}
