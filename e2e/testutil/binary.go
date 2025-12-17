//go:build e2e

package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// BuildTestBinary builds the binary for testing and returns its path
func BuildTestBinary(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "list-to-map")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = filepath.Join(GetProjectRoot(t), "cmd")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build test binary: %v\nOutput: %s", err, output)
	}

	return binPath
}

// GetProjectRoot returns the project root directory
func GetProjectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// From e2e/ directory, go up one level
	return filepath.Dir(wd)
}
