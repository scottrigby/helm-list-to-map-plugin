//go:build !e2e

package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/crd"
)

// SetupTestEnv creates an isolated HELM_CONFIG_HOME for tests
func SetupTestEnv(t *testing.T) string {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("HELM_CONFIG_HOME", configDir)

	pluginDir := filepath.Join(configDir, "list-to-map")
	if err := os.MkdirAll(filepath.Join(pluginDir, "crds"), 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	return pluginDir
}

// ResetGlobalState resets global state between tests
func ResetGlobalState(t *testing.T) {
	t.Helper()
	crd.ResetGlobalRegistry()
}
