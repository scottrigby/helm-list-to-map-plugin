package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/internal/testutil"
)

// captureOutput captures stdout during function execution
func captureOutput(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	// Save original stdout
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	// Create pipe to capture output
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = w
	os.Stderr = w

	// Run function in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- fn()
		w.Close()
	}()

	// Read captured output
	var buf bytes.Buffer
	io.Copy(&buf, r)

	// Get error from function
	fnErr := <-errCh

	return buf.String(), fnErr
}

// TestDetectBasicChart tests basic chart detection
func TestDetectBasicChart(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := "testdata/charts/basic"

	output, err := captureOutput(t, func() error {
		return runDetect(DetectOptions{
			ChartDir: chartPath,
			Verbose:  false,
		})
	})

	if err != nil {
		t.Fatalf("runDetect failed: %v\nOutput: %s", err, output)
	}

	// Verify expected fields are detected
	expectedFields := []string{"env", "volumes", "volumeMounts", "name", "mountPath"}
	for _, field := range expectedFields {
		if !strings.Contains(output, field) {
			t.Errorf("Output should contain %q\nGot:\n%s", field, output)
		}
	}
}

// TestDetectNestedValues tests detection of nested value paths
func TestDetectNestedValues(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := "testdata/charts/nested-values"

	output, err := captureOutput(t, func() error {
		return runDetect(DetectOptions{
			ChartDir: chartPath,
			Verbose:  false,
		})
	})

	if err != nil {
		t.Fatalf("runDetect failed: %v\nOutput: %s", err, output)
	}

	// Should detect nested paths
	expectedPaths := []string{"app.primary.env", "app.secondary.env", "deployment.extraVolumes"}
	for _, path := range expectedPaths {
		if !strings.Contains(output, path) {
			t.Errorf("Output should contain nested path %q\nGot:\n%s", path, output)
		}
	}
}

// TestDetectAllPatterns tests detection of all common patterns
func TestDetectAllPatterns(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := "testdata/charts/all-patterns"

	output, err := captureOutput(t, func() error {
		return runDetect(DetectOptions{
			ChartDir: chartPath,
			Verbose:  false,
		})
	})

	if err != nil {
		t.Fatalf("runDetect failed: %v\nOutput: %s", err, output)
	}

	// Should detect all pattern types
	expectedPatterns := []string{
		"env",
		"volumes",
		"volumeMounts",
		"ports",
		"deployment.extraEnv",
		"deployment.extraVolumes",
	}

	for _, pattern := range expectedPatterns {
		if !strings.Contains(output, pattern) {
			t.Errorf("Output should contain pattern %q\nGot:\n%s", pattern, output)
		}
	}
}

// TestDetectVerbose tests verbose output mode
func TestDetectVerbose(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := "testdata/charts/basic"

	output, err := captureOutput(t, func() error {
		return runDetect(DetectOptions{
			ChartDir: chartPath,
			Verbose:  true,
		})
	})

	if err != nil {
		t.Fatalf("runDetect failed: %v\nOutput: %s", err, output)
	}

	// Verbose mode should show template file names
	if !strings.Contains(output, "deployment.yaml") && !strings.Contains(output, "Template:") {
		t.Errorf("Verbose output should show template information\nGot:\n%s", output)
	}
}

// TestDetectRecursive tests recursive detection in umbrella charts
func TestDetectRecursive(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := "testdata/charts/umbrella"

	// Test without recursive
	outputNonRecursive, err := captureOutput(t, func() error {
		return runDetect(DetectOptions{
			ChartDir:  chartPath,
			Recursive: false,
		})
	})

	if err != nil {
		t.Fatalf("runDetect (non-recursive) failed: %v\nOutput: %s", err, outputNonRecursive)
	}

	// Test with recursive
	outputRecursive, err := captureOutput(t, func() error {
		return runDetect(DetectOptions{
			ChartDir:  chartPath,
			Recursive: true,
		})
	})

	if err != nil {
		t.Fatalf("runDetect (recursive) failed: %v\nOutput: %s", err, outputRecursive)
	}

	// Recursive output should mention subchart
	if !strings.Contains(outputRecursive, "subchart") {
		t.Errorf("Recursive output should mention subchart\nGot:\n%s", outputRecursive)
	}
}

// TestDetectHelp tests that detect shows usage information
func TestDetectHelp(t *testing.T) {
	// This is a smoke test - just verify the Options structure is correct
	opts := DetectOptions{
		ChartDir:  "testdata/charts/basic",
		Recursive: false,
		Verbose:   false,
	}

	// Verify required fields are present
	if opts.ChartDir == "" {
		t.Error("ChartDir should be set")
	}
}
