package server

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/ollama/ollama/envconfig"
	"github.com/ollama/ollama/manifest"
	"github.com/ollama/ollama/types/model"
)

// allowRegistryHost reports whether this installation may open a network
// connection to the registry that hosts n. OwnBot ships local-only: registry
// traffic is refused unless the operator opts a host in with OLLAMA_REMOTES,
// which keeps pulls and pushes off registry.ollama.ai by default. Loopback
// registries stay reachable so a local registry can still be used directly.
func allowRegistryHost(n model.Name) error {
	host := n.BaseURL().Hostname()
	if isLoopbackRegistryHost(host) {
		return nil
	}
	for _, remote := range envconfig.Remotes() {
		if strings.EqualFold(remote, host) {
			return nil
		}
	}
	return fmt.Errorf(
		"registry %q is not enabled: this build is local-only by default, set OLLAMA_REMOTES=%s to allow it",
		host,
		host,
	)
}

func isLoopbackRegistryHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// localModelComplete reports whether n's manifest and every blob it
// references are already on disk, so a pull can succeed without any registry
// connection.
func localModelComplete(n model.Name) bool {
	mf, err := manifest.ParseNamedManifest(n)
	if err != nil {
		return false
	}
	layers := append([]manifest.Layer{}, mf.Layers...)
	if mf.Config.Digest != "" {
		layers = append(layers, mf.Config)
	}
	if len(layers) == 0 {
		return false
	}
	for _, layer := range layers {
		path, err := manifest.BlobsPath(layer.Digest)
		if err != nil {
			return false
		}
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}
