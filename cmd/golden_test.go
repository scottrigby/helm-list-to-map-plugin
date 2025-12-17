package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "update golden test files")

// TestDetectGolden tests detect command output against golden files
// Note: detect output order is non-deterministic, so we compare line sets
func TestDetectGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping golden test in short mode")
	}

	binPath := buildTestBinary(t)

	tests := []struct {
		name       string
		chart      string
		goldenFile string
	}{
		{
			name:       "basic chart",
			chart:      "charts/basic",
			goldenFile: "detect/basic.txt",
		},
		{
			name:       "nested-values chart",
			chart:      "charts/nested-values",
			goldenFile: "detect/nested-values.txt",
		},
		{
			name:       "all-patterns chart",
			chart:      "charts/all-patterns",
			goldenFile: "detect/all-patterns.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chartPath := getTestdataPath(t, tt.chart)
			goldenPath := getTestdataPath(t, filepath.Join("golden", tt.goldenFile))

			cmd := exec.Command(binPath, "detect", "--chart", chartPath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("detect command failed: %v\nOutput: %s", err, output)
			}

			actual := string(output)

			if *updateGolden {
				// Update golden file (sorted for consistency)
				sorted := sortOutputLines(actual)
				if err := os.WriteFile(goldenPath, []byte(sorted), 0644); err != nil {
					t.Fatalf("failed to update golden file: %v", err)
				}
				t.Logf("Updated golden file: %s", goldenPath)
				return
			}

			// Compare with golden file (order-independent)
			expected, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("failed to read golden file %s: %v", goldenPath, err)
			}

			// Compare as sets of lines (order-independent)
			actualLines := parseDetectOutput(actual)
			expectedLines := parseDetectOutput(string(expected))

			// Check all expected lines are present
			for _, line := range expectedLines {
				found := false
				for _, actualLine := range actualLines {
					if line == actualLine {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing expected line: %q", line)
				}
			}

			// Check no unexpected lines
			for _, line := range actualLines {
				found := false
				for _, expectedLine := range expectedLines {
					if line == expectedLine {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("unexpected line in output: %q", line)
				}
			}
		})
	}
}

// parseDetectOutput parses detect output into individual detected fields
func parseDetectOutput(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "Detected convertible arrays:" && !strings.HasPrefix(line, "No ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// sortOutputLines sorts the output lines for deterministic golden files
func sortOutputLines(s string) string {
	lines := strings.Split(s, "\n")
	// Find the header and content lines
	var header string
	var content []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Detected") || strings.HasPrefix(trimmed, "No ") {
			header = trimmed
		} else {
			content = append(content, line)
		}
	}

	// Sort content lines
	sortStrings(content)

	// Reconstruct
	var result strings.Builder
	if header != "" {
		result.WriteString(header)
		result.WriteString("\n")
	}
	for _, line := range content {
		result.WriteString(line)
		result.WriteString("\n")
	}
	return result.String()
}

// sortStrings sorts a slice of strings in place
func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// TestConvertDryRunGolden tests convert --dry-run output format
func TestConvertDryRunGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping golden test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/basic"))

	cmd := exec.Command(binPath, "convert", "--chart", chartPath, "--dry-run")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert --dry-run failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Verify key elements are present in dry-run output
	expectedElements := []string{
		"dry-run",
		"env",
		"volumes",
		"volumeMounts",
	}

	for _, elem := range expectedElements {
		if !strings.Contains(strings.ToLower(outputStr), strings.ToLower(elem)) {
			t.Errorf("dry-run output should contain %q\nGot:\n%s", elem, outputStr)
		}
	}
}

// TestVerboseGolden tests verbose output format
func TestVerboseGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping golden test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := getTestdataPath(t, "charts/basic")

	cmd := exec.Command(binPath, "detect", "--chart", chartPath, "-v")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detect -v failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Verbose output should include template file references
	if !strings.Contains(outputStr, "deployment.yaml") {
		t.Errorf("verbose output should mention template files\nGot:\n%s", outputStr)
	}

	// Should still have the detected fields
	if !strings.Contains(outputStr, "env") {
		t.Errorf("verbose output should contain detected fields\nGot:\n%s", outputStr)
	}
}

// TestConvertOutputFormat tests the convert command output format
func TestConvertOutputFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping golden test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/basic"))

	cmd := exec.Command(binPath, "convert", "--chart", chartPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Convert output should mention what was converted
	expectedElements := []string{
		"convert",
		"env",
	}

	hasAny := false
	for _, elem := range expectedElements {
		if strings.Contains(strings.ToLower(outputStr), strings.ToLower(elem)) {
			hasAny = true
			break
		}
	}

	if !hasAny {
		t.Logf("convert output: %s", outputStr)
	}

	// Verify the conversion actually happened
	valuesPath := filepath.Join(chartPath, "values.yaml")
	valuesContent, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("failed to read converted values.yaml: %v", err)
	}

	// Should have map format (DB_HOST: as key)
	if !strings.Contains(string(valuesContent), "DB_HOST:") {
		t.Error("converted values.yaml should have DB_HOST as map key")
	}

	// Should NOT have array format
	if strings.Contains(string(valuesContent), "- name: DB_HOST") {
		t.Error("converted values.yaml should NOT have array format")
	}
}

// TestRecursiveDetectFormat tests recursive detect output format
func TestRecursiveDetectFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping golden test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := getTestdataPath(t, "charts/umbrella")

	cmd := exec.Command(binPath, "detect", "--chart", chartPath, "--recursive")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detect --recursive failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Should mention subcharts
	if !strings.Contains(outputStr, "subchart") {
		t.Errorf("recursive output should mention subcharts\nGot:\n%s", outputStr)
	}
}
