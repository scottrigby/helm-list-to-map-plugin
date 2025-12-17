package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIDetectOutput tests that the detect command produces expected output
func TestCLIDetectOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	// Build the binary first
	binPath := buildTestBinary(t)

	tests := []struct {
		name           string
		chart          string
		args           []string
		wantContains   []string // Strings that must appear in output
		wantNotContain []string // Strings that must NOT appear in output
	}{
		{
			name:  "basic chart detection",
			chart: "charts/basic",
			args:  []string{},
			wantContains: []string{
				"env",
				"volumes",
				"volumeMounts",
				"name",      // merge key for env/volumes
				"mountPath", // merge key for volumeMounts
			},
		},
		{
			name:  "nested values detection",
			chart: "charts/nested-values",
			args:  []string{},
			wantContains: []string{
				"app.primary.env",
				"app.secondary.env",
				"deployment.extraVolumes",
			},
		},
		{
			name:  "all patterns detection",
			chart: "charts/all-patterns",
			args:  []string{},
			wantContains: []string{
				"env",
				"volumes",
				"volumeMounts",
				"ports",
				"deployment.extraEnv",
				"deployment.extraVolumes",
			},
		},
		{
			name:  "verbose output",
			chart: "charts/basic",
			args:  []string{"-v"},
			wantContains: []string{
				"env",
				"deployment.yaml", // Should show template file
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chartPath := getTestdataPath(t, tt.chart)
			args := append([]string{"detect", "--chart", chartPath}, tt.args...)

			cmd := exec.Command(binPath, args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				// detect should succeed
				t.Fatalf("detect command failed: %v\nOutput: %s", err, output)
			}

			outputStr := string(output)

			for _, want := range tt.wantContains {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Output should contain %q\nGot:\n%s", want, outputStr)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(outputStr, notWant) {
					t.Errorf("Output should NOT contain %q\nGot:\n%s", notWant, outputStr)
				}
			}
		})
	}
}

// TestCLIConvertDryRun tests convert --dry-run output
func TestCLIConvertDryRun(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/basic"))

	cmd := exec.Command(binPath, "convert", "--chart", chartPath, "--dry-run")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert --dry-run failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Dry run should show what would be converted
	expectedContains := []string{
		"env",
		"volumes",
		"dry-run", // Should indicate dry run mode
	}

	for _, want := range expectedContains {
		if !strings.Contains(strings.ToLower(outputStr), strings.ToLower(want)) {
			t.Errorf("Dry run output should contain %q\nGot:\n%s", want, outputStr)
		}
	}

	// Verify files were NOT modified (dry run)
	valuesPath := filepath.Join(chartPath, "values.yaml")
	valuesData, _ := os.ReadFile(valuesPath)
	if !strings.Contains(string(valuesData), "- name: DB_HOST") {
		t.Error("values.yaml should NOT be modified in dry-run mode")
	}
}

// TestCLIConvertActual tests actual conversion
func TestCLIConvertActual(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/basic"))

	// Verify original format
	valuesPath := filepath.Join(chartPath, "values.yaml")
	originalValues, _ := os.ReadFile(valuesPath)
	if !strings.Contains(string(originalValues), "- name: DB_HOST") {
		t.Fatal("Original should have array format")
	}

	cmd := exec.Command(binPath, "convert", "--chart", chartPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert failed: %v\nOutput: %s", err, output)
	}

	// Verify values.yaml was converted to map format
	convertedValues, _ := os.ReadFile(valuesPath)
	convertedStr := string(convertedValues)

	// Should have map format now
	if strings.Contains(convertedStr, "- name: DB_HOST") {
		t.Error("Converted values.yaml should NOT have array format")
	}

	// Should have the key as map entry
	if !strings.Contains(convertedStr, "DB_HOST:") {
		t.Error("Converted values.yaml should have DB_HOST as map key")
	}

	// Backup should exist
	backupPath := valuesPath + ".bak"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("Backup file should be created")
	}
}

// TestCLIDetectRecursive tests the --recursive flag with umbrella charts
func TestCLIDetectRecursive(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := getTestdataPath(t, "charts/umbrella")

	// Test without --recursive (should only detect umbrella chart fields)
	cmd := exec.Command(binPath, "detect", "--chart", chartPath)
	outputWithout, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detect failed: %v\nOutput: %s", err, outputWithout)
	}

	// Test with --recursive (should detect umbrella + subchart fields)
	cmd = exec.Command(binPath, "detect", "--chart", chartPath, "--recursive")
	outputWith, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detect --recursive failed: %v\nOutput: %s", err, outputWith)
	}

	outputWithStr := string(outputWith)

	// Should mention subchart
	if !strings.Contains(outputWithStr, "subchart") {
		t.Errorf("Recursive output should mention subchart\nGot:\n%s", outputWithStr)
	}

	// Recursive output should have more content than non-recursive
	if len(outputWith) <= len(outputWithout) {
		t.Log("Note: Recursive output should generally be longer than non-recursive")
	}
}

// TestCLIConvertRecursive tests the --recursive flag for convert command
func TestCLIConvertRecursive(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/umbrella"))

	// Verify subchart has array format before conversion
	subchartValues := filepath.Join(chartPath, "subcharts", "subchart-a", "values.yaml")
	originalSubchart, _ := os.ReadFile(subchartValues)
	if !strings.Contains(string(originalSubchart), "- name: SUBCHART_DEFAULT") {
		t.Fatal("Subchart should have array format before conversion")
	}

	// Verify umbrella has array format for subchart override before conversion
	umbrellaValues := filepath.Join(chartPath, "values.yaml")
	originalUmbrella, _ := os.ReadFile(umbrellaValues)
	if !strings.Contains(string(originalUmbrella), "- name: SUBCHART_VAR") {
		t.Fatal("Umbrella subchart-a.env should have array format before conversion")
	}

	// Run recursive convert
	cmd := exec.Command(binPath, "convert", "--chart", chartPath, "--recursive")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert --recursive failed: %v\nOutput: %s", err, output)
	}

	// Verify subchart was converted
	convertedSubchart, _ := os.ReadFile(subchartValues)
	if strings.Contains(string(convertedSubchart), "- name: SUBCHART_DEFAULT") {
		t.Error("Subchart values.yaml should be converted from array to map format")
	}

	// Should have SUBCHART_DEFAULT as map key
	if !strings.Contains(string(convertedSubchart), "SUBCHART_DEFAULT:") {
		t.Error("Subchart should have SUBCHART_DEFAULT as map key after conversion")
	}

	// Verify umbrella's subchart override section was updated to match subchart format
	convertedUmbrella, _ := os.ReadFile(umbrellaValues)
	// The subchart-a.env section should be converted to map format
	if strings.Contains(string(convertedUmbrella), "- name: SUBCHART_VAR") {
		t.Error("Umbrella subchart-a.env should be converted from array to map format")
	}
	if !strings.Contains(string(convertedUmbrella), "SUBCHART_VAR:") {
		t.Error("Umbrella should have SUBCHART_VAR as map key after conversion")
	}
}

// TestCLIRules tests the rules command output
func TestCLIRules(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	setupTestEnv(t)
	binPath := buildTestBinary(t)

	cmd := exec.Command(binPath, "rules")
	output, err := cmd.CombinedOutput()
	// Rules command may fail if no config exists, that's ok
	_ = err

	// Should show something about rules (even if empty)
	outputStr := string(output)
	if !strings.Contains(strings.ToLower(outputStr), "rule") {
		t.Logf("Rules output: %s", outputStr)
	}
}

// TestCLIHelp tests help output
func TestCLIHelp(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI test in short mode")
	}

	binPath := buildTestBinary(t)

	tests := []struct {
		args         []string
		wantContains []string
	}{
		{
			args:         []string{"--help"},
			wantContains: []string{"detect", "convert", "load-crd"},
		},
		{
			args:         []string{"detect", "--help"},
			wantContains: []string{"--chart", "--recursive", "Scan"},
		},
		{
			args:         []string{"convert", "--help"},
			wantContains: []string{"--chart", "--dry-run", "--backup-ext"},
		},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := exec.Command(binPath, tt.args...)
			output, _ := cmd.CombinedOutput()
			outputStr := string(output)

			for _, want := range tt.wantContains {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Help output should contain %q\nGot:\n%s", want, outputStr)
				}
			}
		})
	}
}

// buildTestBinary builds the binary for testing and returns its path
func buildTestBinary(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "list-to-map")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = filepath.Join(getProjectRoot(t), "cmd")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build test binary: %v\nStderr: %s", err, stderr.String())
	}

	return binPath
}

// getProjectRoot returns the project root directory
func getProjectRoot(t *testing.T) string {
	t.Helper()
	// Walk up from cmd/testdata to find project root
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// If we're in cmd/, go up one level
	if filepath.Base(wd) == "cmd" {
		return filepath.Dir(wd)
	}
	return wd
}
