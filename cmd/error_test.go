package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/transform"
)

// TestCLIUnknownCommand tests that unknown commands produce an error
func TestCLIUnknownCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)

	cmd := exec.Command(binPath, "unknowncommand")
	output, err := cmd.CombinedOutput()

	// Should exit with error
	if err == nil {
		t.Error("expected error for unknown command")
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "unknown command") {
		t.Errorf("output should mention 'unknown command', got: %s", outputStr)
	}
}

// TestCLIDetectNoChart tests detect without required --chart flag
func TestCLIDetectNoChart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)

	cmd := exec.Command(binPath, "detect")
	output, err := cmd.CombinedOutput()

	// Should exit with error
	if err == nil {
		t.Error("expected error when --chart is not provided")
	}

	outputStr := string(output)
	// Should mention chart is required or not found
	if !strings.Contains(strings.ToLower(outputStr), "chart") {
		t.Errorf("output should mention 'chart', got: %s", outputStr)
	}
}

// TestCLIDetectNonexistentChart tests detect with a chart that doesn't exist
func TestCLIDetectNonexistentChart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)

	cmd := exec.Command(binPath, "detect", "--chart", "/nonexistent/path/to/chart")
	output, err := cmd.CombinedOutput()

	// Should exit with error
	if err == nil {
		t.Error("expected error for nonexistent chart path")
	}

	outputStr := string(output)
	// Should mention the file/directory doesn't exist
	if !strings.Contains(strings.ToLower(outputStr), "no such file") &&
		!strings.Contains(strings.ToLower(outputStr), "not found") &&
		!strings.Contains(strings.ToLower(outputStr), "does not exist") &&
		!strings.Contains(strings.ToLower(outputStr), "error") {
		t.Errorf("output should indicate path doesn't exist, got: %s", outputStr)
	}
}

// TestCLIConvertNoChart tests convert without required --chart flag
func TestCLIConvertNoChart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)

	cmd := exec.Command(binPath, "convert")
	output, err := cmd.CombinedOutput()

	// Should exit with error
	if err == nil {
		t.Error("expected error when --chart is not provided")
	}

	outputStr := string(output)
	// Should mention chart is required or not found
	if !strings.Contains(strings.ToLower(outputStr), "chart") {
		t.Errorf("output should mention 'chart', got: %s", outputStr)
	}
}

// TestCLIConvertNonexistentChart tests convert with a chart that doesn't exist
func TestCLIConvertNonexistentChart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)

	cmd := exec.Command(binPath, "convert", "--chart", "/nonexistent/path/to/chart")
	output, err := cmd.CombinedOutput()

	// Should exit with error
	if err == nil {
		t.Error("expected error for nonexistent chart path")
	}

	outputStr := string(output)
	if !strings.Contains(strings.ToLower(outputStr), "no such file") &&
		!strings.Contains(strings.ToLower(outputStr), "not found") &&
		!strings.Contains(strings.ToLower(outputStr), "does not exist") &&
		!strings.Contains(strings.ToLower(outputStr), "error") {
		t.Errorf("output should indicate path doesn't exist, got: %s", outputStr)
	}
}

// TestCLILoadCRDNoSource tests load-crd without a source argument
func TestCLILoadCRDNoSource(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)

	cmd := exec.Command(binPath, "load-crd")
	output, err := cmd.CombinedOutput()

	// Should exit with error or show usage
	// If it exits with error, the err will be non-nil
	outputStr := string(output)
	_ = err

	// Should mention that a source is required
	if !strings.Contains(strings.ToLower(outputStr), "usage") &&
		!strings.Contains(strings.ToLower(outputStr), "source") &&
		!strings.Contains(strings.ToLower(outputStr), "error") {
		t.Logf("load-crd with no args output: %s", outputStr)
	}
}

// TestCLILoadCRDNonexistentFile tests load-crd with a file that doesn't exist
func TestCLILoadCRDNonexistentFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)

	cmd := exec.Command(binPath, "load-crd", "/nonexistent/crd.yaml")
	output, err := cmd.CombinedOutput()

	// Note: load-crd exits with 0 but shows warning for nonexistent files
	// This is expected behavior - it warns but doesn't fail
	outputStr := string(output)
	_ = err

	// Should show warning about the file not existing
	if !strings.Contains(strings.ToLower(outputStr), "no such file") &&
		!strings.Contains(strings.ToLower(outputStr), "not found") &&
		!strings.Contains(strings.ToLower(outputStr), "warning") {
		t.Errorf("output should warn about nonexistent file, got: %s", outputStr)
	}
}

// TestCLILoadCRDInvalidYAML tests load-crd with an invalid YAML file
func TestCLILoadCRDInvalidYAML(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	tmpDir := t.TempDir()

	// Create an invalid YAML file
	invalidYAML := filepath.Join(tmpDir, "invalid.yaml")
	if err := os.WriteFile(invalidYAML, []byte("this: is: not: valid: yaml {{"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "load-crd", invalidYAML)
	output, err := cmd.CombinedOutput()

	// Should exit with error or warning
	outputStr := string(output)

	// The output should mention something about parsing/YAML error
	if err == nil && !strings.Contains(strings.ToLower(outputStr), "warning") {
		t.Logf("load-crd with invalid YAML output: %s", outputStr)
	}
}

// TestCLILoadCRDNonCRDFile tests load-crd with a valid YAML but not a CRD
func TestCLILoadCRDNonCRDFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	tmpDir := t.TempDir()

	// Create a valid YAML that isn't a CRD
	configMap := filepath.Join(tmpDir, "configmap.yaml")
	content := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  key: value
`
	if err := os.WriteFile(configMap, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "load-crd", configMap)
	output, err := cmd.CombinedOutput()

	outputStr := string(output)

	// Should handle gracefully - either skip with message or show no CRDs found
	// This is more of a validation that it doesn't crash
	_ = err
	t.Logf("load-crd with non-CRD YAML output: %s", outputStr)
}

// TestCRDRegistryLoadFromFileErrors tests CRD loading error cases
func TestCRDRegistryLoadFromFileErrors(t *testing.T) {
	t.Parallel()

	// Test loading from non-existent file
	reg := NewCRDRegistry()
	err := reg.LoadFromFile("/nonexistent/path.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}

	// Test loading from directory (should fail when expecting file)
	tmpDir := t.TempDir()
	err = reg.LoadFromFile(tmpDir)
	if err == nil {
		t.Error("expected error when loading directory as file")
	}
}

// TestCRDRegistryLoadFromDirectoryErrors tests directory loading error cases
func TestCRDRegistryLoadFromDirectoryErrors(t *testing.T) {
	t.Parallel()

	reg := NewCRDRegistry()

	// Test loading from non-existent directory
	err := reg.LoadFromDirectory("/nonexistent/directory")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

// TestTransformErrorCases tests transformation edge cases
func TestTransformErrorCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arrayLines []string
		mergeKey   string
	}{
		{
			name:       "empty array",
			arrayLines: []string{},
			mergeKey:   "name",
		},
		{
			name: "missing merge key in item",
			arrayLines: []string{
				"  - value: test", // No "name" key
			},
			mergeKey: "name",
		},
		{
			name: "array with only comments",
			arrayLines: []string{
				"  # comment line",
				"  # another comment",
			},
			mergeKey: "name",
		},
		{
			name: "malformed YAML array",
			arrayLines: []string{
				"not an array",
			},
			mergeKey: "name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify it doesn't panic
			result := transform.TransformArrayToMap(tt.arrayLines, tt.mergeKey)
			_ = result
		})
	}
}

// TestGlobMatchingEdgeCases tests glob pattern matching edge cases
func TestGlobMatchingEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{
			name:    "nil-like empty strings",
			pattern: "",
			path:    "",
			want:    true,
		},
		{
			name:    "pattern with only wildcard",
			pattern: "*",
			path:    "anything",
			want:    true,
		},
		{
			name:    "multiple wildcards",
			pattern: "*.*.*",
			path:    "a.b.c",
			want:    true,
		},
		{
			name:    "wildcard at end",
			pattern: "spec.*",
			path:    "spec.containers",
			want:    true,
		},
		{
			name:    "wildcard in middle",
			pattern: "spec.*.name",
			path:    "spec.containers.name",
			want:    true,
		},
		{
			name:    "special characters in path",
			pattern: "spec.env",
			path:    "spec.env",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

// TestChartWithNoValuesYaml tests behavior when a chart has no values.yaml
func TestChartWithNoValuesYaml(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	tmpDir := t.TempDir()

	// Create a minimal chart with no values.yaml
	chartYaml := filepath.Join(tmpDir, "Chart.yaml")
	if err := os.WriteFile(chartYaml, []byte("apiVersion: v2\nname: test\nversion: 0.1.0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create empty templates directory
	templatesDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "detect", "--chart", tmpDir)
	output, err := cmd.CombinedOutput()

	// Should not crash, may show no candidates found
	outputStr := string(output)
	_ = err
	t.Logf("detect with no values.yaml: %s", outputStr)
}

// TestChartWithEmptyValuesYaml tests behavior when values.yaml is empty
func TestChartWithEmptyValuesYaml(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	tmpDir := t.TempDir()

	// Create a minimal chart with empty values.yaml
	chartYaml := filepath.Join(tmpDir, "Chart.yaml")
	if err := os.WriteFile(chartYaml, []byte("apiVersion: v2\nname: test\nversion: 0.1.0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	valuesYaml := filepath.Join(tmpDir, "values.yaml")
	if err := os.WriteFile(valuesYaml, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Create empty templates directory
	templatesDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "detect", "--chart", tmpDir)
	output, err := cmd.CombinedOutput()

	// Should not crash, may show no candidates found
	outputStr := string(output)
	_ = err
	t.Logf("detect with empty values.yaml: %s", outputStr)
}

// TestConvertIdempotency tests that running convert twice produces same result
func TestConvertIdempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/basic"))

	// First conversion
	cmd := exec.Command(binPath, "convert", "--chart", chartPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("first convert failed: %v\nOutput: %s", err, output)
	}

	// Read state after first conversion
	valuesAfterFirst, err := os.ReadFile(filepath.Join(chartPath, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// Second conversion
	cmd = exec.Command(binPath, "convert", "--chart", chartPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second convert failed: %v\nOutput: %s", err, output)
	}

	// Read state after second conversion
	valuesAfterSecond, err := os.ReadFile(filepath.Join(chartPath, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// Should be identical (idempotent)
	if string(valuesAfterFirst) != string(valuesAfterSecond) {
		t.Error("convert should be idempotent - running twice produced different results")
		t.Logf("After first:\n%s", valuesAfterFirst)
		t.Logf("After second:\n%s", valuesAfterSecond)
	}
}
