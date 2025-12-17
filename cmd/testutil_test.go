package main

import (
	"os"
	"path/filepath"
	"testing"
)

// setupTestEnv creates an isolated HELM_CONFIG_HOME for tests
func setupTestEnv(t *testing.T) string {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("HELM_CONFIG_HOME", configDir)

	// Create plugin config structure
	pluginDir := filepath.Join(configDir, "list-to-map")
	if err := os.MkdirAll(filepath.Join(pluginDir, "crds"), 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	return pluginDir
}

// copyChart copies a test chart to a temp directory for modification
func copyChart(t *testing.T, srcChart string) string {
	t.Helper()
	dst := t.TempDir()

	err := filepath.Walk(srcChart, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(srcChart, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("failed to copy chart: %v", err)
	}
	return dst
}

// resetGlobalState resets global state between tests
func resetGlobalState(t *testing.T) {
	t.Helper()
	globalCRDRegistry = NewCRDRegistry()
}

// getTestdataPath returns the absolute path to a testdata file
func getTestdataPath(t *testing.T, relativePath string) string {
	t.Helper()
	// Get the directory of the current test file
	_, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	return filepath.Join("testdata", relativePath)
}
