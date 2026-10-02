package server

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/manifest"
	"github.com/ollama/ollama/types/model"
)

func TestAllowRegistryHostIsLocalOnlyByDefault(t *testing.T) {
	t.Setenv("OLLAMA_REMOTES", "")
	n := model.ParseName("library/test-model")

	err := allowRegistryHost(n)
	if err == nil {
		t.Fatal("registry.ollama.ai must be refused without OLLAMA_REMOTES")
	}
	if !strings.Contains(err.Error(), "OLLAMA_REMOTES") {
		t.Fatalf("error = %q, want OLLAMA_REMOTES guidance", err)
	}

	t.Setenv("OLLAMA_REMOTES", "Registry.Ollama.AI")
	if err := allowRegistryHost(n); err != nil {
		t.Fatalf("opted-in registry refused: %v", err)
	}
}

func TestAllowRegistryHostAllowsLoopback(t *testing.T) {
	t.Setenv("OLLAMA_REMOTES", "")
	for _, name := range []string{"127.0.0.1:5000/ns/model", "localhost:5000/ns/model"} {
		if err := allowRegistryHost(model.ParseName(name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestPullModelRefusesDisabledRegistry(t *testing.T) {
	t.Setenv("OLLAMA_MODELS", t.TempDir())
	t.Setenv("OLLAMA_REMOTES", "")

	err := PullModel(t.Context(), "definitely-not-local", &registryOptions{}, func(api.ProgressResponse) {})
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("PullModel = %v, want disabled-registry error", err)
	}
}

func TestPushModelRefusesDisabledRegistry(t *testing.T) {
	t.Setenv("OLLAMA_MODELS", t.TempDir())
	t.Setenv("OLLAMA_REMOTES", "")

	err := PushModel(t.Context(), "definitely-not-local", &registryOptions{}, func(api.ProgressResponse) {})
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("PushModel = %v, want disabled-registry error", err)
	}
}

func TestPullModelShortCircuitsFullyLocalModel(t *testing.T) {
	t.Setenv("OLLAMA_MODELS", t.TempDir())
	t.Setenv("OLLAMA_REMOTES", "")

	n := model.ParseName("local/model")
	blob := []byte("fully local model blob")
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(blob))
	blobPath, err := manifest.BlobsPath(digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	config := manifest.Layer{MediaType: "application/vnd.docker.container.image.v1+json", Digest: digest, Size: int64(len(blob))}
	layers := []manifest.Layer{{MediaType: "application/vnd.ollama.image.model", Digest: digest, Size: int64(len(blob))}}
	if err := manifest.WriteManifest(n, config, layers); err != nil {
		t.Fatal(err)
	}

	statuses := make([]string, 0, 1)
	if err := PullModel(t.Context(), n.String(), &registryOptions{}, func(r api.ProgressResponse) {
		statuses = append(statuses, r.Status)
	}); err != nil {
		t.Fatalf("local pull = %v", err)
	}
	if len(statuses) == 0 || statuses[len(statuses)-1] != "success" {
		t.Fatalf("statuses = %v, want success", statuses)
	}
}

func TestLocalModelCompleteRequiresEveryBlob(t *testing.T) {
	t.Setenv("OLLAMA_MODELS", t.TempDir())

	n := model.ParseName("local/incomplete")
	config := manifest.Layer{MediaType: "application/vnd.docker.container.image.v1+json", Digest: "sha256:missing", Size: 5}
	layers := []manifest.Layer{{MediaType: "application/vnd.ollama.image.model", Digest: "sha256:missing", Size: 5}}
	if err := manifest.WriteManifest(n, config, layers); err != nil {
		t.Fatal(err)
	}
	if localModelComplete(n) {
		t.Fatal("model without blobs must not be treated as complete")
	}
}
