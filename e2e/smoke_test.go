//go:build e2e

package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/e2e/testutil"
)

// TestBinaryBuildsAndRuns verifies the binary builds and executes
func TestBinaryBuildsAndRuns(t *testing.T) {
	binPath := testutil.BuildTestBinary(t)

	tests := []struct {
		name string
		args []string
	}{
		{"help", []string{"--help"}},
		{"detect help", []string{"detect", "--help"}},
		{"convert help", []string{"convert", "--help"}},
		{"rules command", []string{"rules"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(binPath, tt.args...)
			output, _ := cmd.CombinedOutput()

			// Help commands exit 0 or show usage
			outputStr := string(output)

			// Verify we got some output
			if len(outputStr) == 0 {
				t.Error("Expected output from command")
			}

			// Basic smoke test - no crash, produces output
			t.Logf("Command output length: %d bytes", len(outputStr))
		})
	}
}

// TestBinaryDetectBasic verifies detect command works end-to-end
func TestBinaryDetectBasic(t *testing.T) {
	binPath := testutil.BuildTestBinary(t)
	chartPath := testutil.GetProjectRoot(t) + "/e2e/testdata/charts/basic"

	cmd := exec.Command(binPath, "detect", "--chart", chartPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detect failed: %v\nOutput: %s", err, output)
	}

	// Verify some output was produced
	if len(output) == 0 {
		t.Error("Expected detect to produce output")
	}

	// Basic check - should mention some fields
	if !strings.Contains(string(output), "env") && !strings.Contains(string(output), "volumes") {
		t.Error("Expected detect output to mention convertible fields")
	}
}
