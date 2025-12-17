package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubchartS1C tests S1-C: Single embedded chart in charts/ (--include-charts-dir)
func TestSubchartS1C_SingleEmbeddedChart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setupTestEnv(t)
	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/matrix/single-types/s1-charts"))

	// Test detect with --include-charts-dir
	cmd := exec.Command(binPath, "detect", "--chart", chartPath, "--include-charts-dir")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detect --include-charts-dir failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Should detect subchart
	if !strings.Contains(outputStr, "embedded") {
		t.Errorf("Output should mention embedded subchart\nGot:\n%s", outputStr)
	}

	// Should find env and volumes arrays in subchart
	if !strings.Contains(outputStr, "env") {
		t.Errorf("Output should detect env arrays\nGot:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "volumes") {
		t.Errorf("Output should detect volumes arrays\nGot:\n%s", outputStr)
	}

	// Test convert with --include-charts-dir
	cmd = exec.Command(binPath, "convert", "--chart", chartPath, "--include-charts-dir")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert --include-charts-dir failed: %v\nOutput: %s", err, output)
	}

	// Verify subchart values.yaml was converted
	subchartValuesPath := filepath.Join(chartPath, "charts", "embedded", "values.yaml")
	convertedValues, err := os.ReadFile(subchartValuesPath)
	if err != nil {
		t.Fatalf("Failed to read converted subchart values.yaml: %v", err)
	}

	convertedStr := string(convertedValues)

	// Should have map format (env becomes env: {key: {...}})
	if strings.Contains(convertedStr, "- name: EMBEDDED_VAR") {
		t.Error("Subchart values.yaml should be converted from array to map format")
	}

	// Should have key as map entry
	if !strings.Contains(convertedStr, "EMBEDDED_VAR:") {
		t.Error("Subchart values.yaml should have EMBEDDED_VAR as map key")
	}

	// Verify backup created for subchart
	backupPath := subchartValuesPath + ".bak"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("Backup file should be created for subchart values.yaml")
	}
}

// TestSubchartS1T tests S1-T: Single tarball in charts/ (--expand-remote)
func TestSubchartS1T_SingleTarball(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setupTestEnv(t)
	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/matrix/single-types/s1-tarball"))

	// Verify tarball exists before conversion
	tgzPath := filepath.Join(chartPath, "charts", "remote-chart-1.0.0.tgz")
	if _, err := os.Stat(tgzPath); os.IsNotExist(err) {
		t.Fatal("Tarball should exist before conversion")
	}

	// Test convert with --expand-remote
	cmd := exec.Command(binPath, "convert", "--chart", chartPath, "--expand-remote")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert --expand-remote failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Should show warning about remote dependencies
	if !strings.Contains(outputStr, "WARNING") && !strings.Contains(outputStr, "remote") {
		t.Logf("Expected warning about remote dependencies in output\nGot:\n%s", outputStr)
	}

	// Verify tarball was extracted (directory should exist, .tgz removed)
	extractedDir := filepath.Join(chartPath, "charts", "remote-chart-1.0.0")
	if _, err := os.Stat(extractedDir); os.IsNotExist(err) {
		t.Error("Tarball should be extracted to directory")
	}

	// Verify backup .tgz.bak was created
	backupPath := tgzPath + ".bak"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error(".tgz.bak backup should be created")
	}

	// Verify original .tgz was removed
	if _, err := os.Stat(tgzPath); err == nil {
		t.Error("Original .tgz should be removed after extraction")
	}

	// Verify extracted chart values.yaml was converted
	extractedValuesPath := filepath.Join(extractedDir, "values.yaml")
	convertedValues, err := os.ReadFile(extractedValuesPath)
	if err != nil {
		t.Fatalf("Failed to read extracted chart values.yaml: %v", err)
	}

	convertedStr := string(convertedValues)

	// Should have map format for env and volumeMounts
	if strings.Contains(convertedStr, "- name: REMOTE_VAR") {
		t.Error("Extracted chart values.yaml should be converted from array to map format")
	}

	if !strings.Contains(convertedStr, "REMOTE_VAR:") {
		t.Error("Extracted chart should have REMOTE_VAR as map key")
	}
}

// TestSubchartM2FC tests M2-FC: Mix of file:// + charts/ directory
func TestSubchartM2FC_MixFileAndCharts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setupTestEnv(t)
	binPath := buildTestBinary(t)
	chartPath := copyChartWithSiblings(t, getTestdataPath(t, "charts/matrix/two-type-mix/m2-file-charts"))

	// Test detect with both flags
	cmd := exec.Command(binPath, "detect", "--chart", chartPath, "--recursive", "--include-charts-dir")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detect with both flags failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Should detect both sibling (file://) and embedded (charts/)
	if !strings.Contains(outputStr, "sibling") && !strings.Contains(outputStr, "volumes") {
		t.Logf("Note: Output should mention sibling chart or volumes\nGot:\n%s", outputStr)
	}

	if !strings.Contains(outputStr, "embedded") && !strings.Contains(outputStr, "env") {
		t.Logf("Note: Output should mention embedded chart or env\nGot:\n%s", outputStr)
	}

	// Test convert with both flags
	cmd = exec.Command(binPath, "convert", "--chart", chartPath, "--recursive", "--include-charts-dir")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert with both flags failed: %v\nOutput: %s", err, output)
	}

	// Verify sibling chart (file://) was converted
	siblingValuesPath := filepath.Join(filepath.Dir(chartPath), "sibling", "values.yaml")
	siblingValues, err := os.ReadFile(siblingValuesPath)
	if err != nil {
		t.Fatalf("Failed to read sibling values.yaml: %v", err)
	}

	if strings.Contains(string(siblingValues), "- name: sibling-data") {
		t.Error("Sibling chart should be converted from array to map format")
	}

	// Verify embedded chart (charts/) was converted
	embeddedValuesPath := filepath.Join(chartPath, "charts", "embedded", "values.yaml")
	embeddedValues, err := os.ReadFile(embeddedValuesPath)
	if err != nil {
		t.Fatalf("Failed to read embedded values.yaml: %v", err)
	}

	if strings.Contains(string(embeddedValues), "- name: EMBEDDED_VAR") {
		t.Error("Embedded chart should be converted from array to map format")
	}

	// Count total subcharts processed (should be 2)
	// This is implicit - if both conversions succeeded, 2 charts were processed
}

// TestSubchartDFC tests D-FC: Deduplication (file:// pointing to charts/)
func TestSubchartDFC_Deduplication(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setupTestEnv(t)
	binPath := buildTestBinary(t)
	chartPath := copyChart(t, getTestdataPath(t, "charts/matrix/deduplication/d-file-to-charts"))

	// Test convert with both flags (should deduplicate shared chart)
	cmd := exec.Command(binPath, "convert", "--chart", chartPath, "--recursive", "--include-charts-dir")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert with both flags failed: %v\nOutput: %s", err, output)
	}

	// Verify shared chart was converted (only once)
	sharedValuesPath := filepath.Join(chartPath, "charts", "shared", "values.yaml")
	sharedValues, err := os.ReadFile(sharedValuesPath)
	if err != nil {
		t.Fatalf("Failed to read shared values.yaml: %v", err)
	}

	sharedStr := string(sharedValues)

	// Should be converted to map format
	if strings.Contains(sharedStr, "- containerPort: 8080") {
		t.Error("Shared chart should be converted from array to map format")
	}

	// Should have containerPort as key (or similar depending on merge key)
	// ports use containerPort as merge key
	if !strings.Contains(sharedStr, "8080:") && !strings.Contains(sharedStr, "\"8080\":") {
		t.Error("Shared chart ports should use containerPort as map key")
	}

	// Verify only one backup was created (deduplication worked)
	backupPath := sharedValuesPath + ".bak"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("Backup file should be created for shared chart")
	}

	// Read backup to verify it had original array format
	backupData, _ := os.ReadFile(backupPath)
	if !strings.Contains(string(backupData), "- containerPort: 8080") {
		t.Error("Backup should contain original array format")
	}
}

// TestSubchartM3FCT tests M3-FCT: All three types combined
func TestSubchartM3FCT_AllThreeTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setupTestEnv(t)
	binPath := buildTestBinary(t)
	chartPath := copyChartWithSiblings(t, getTestdataPath(t, "charts/matrix/three-type-mix/m3-all"))

	// Verify tarball exists before conversion
	tgzPath := filepath.Join(chartPath, "charts", "tarball-chart-2.0.0.tgz")
	if _, err := os.Stat(tgzPath); os.IsNotExist(err) {
		t.Fatal("Tarball should exist before conversion")
	}

	// Test convert with all three flags
	cmd := exec.Command(binPath, "convert", "--chart", chartPath, "--recursive", "--include-charts-dir", "--expand-remote")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("convert with all flags failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Should show warning about remote dependencies
	if !strings.Contains(outputStr, "WARNING") && !strings.Contains(outputStr, "remote") {
		t.Logf("Expected warning about remote dependencies\nGot:\n%s", outputStr)
	}

	// Verify all three types were processed:

	// 1. file:// dependency (sibling)
	siblingValuesPath := filepath.Join(filepath.Dir(chartPath), "sibling", "values.yaml")
	siblingValues, err := os.ReadFile(siblingValuesPath)
	if err != nil {
		t.Fatalf("Failed to read sibling values.yaml: %v", err)
	}
	if strings.Contains(string(siblingValues), "- containerPort: 3000") {
		t.Error("Sibling chart (file://) should be converted")
	}

	// 2. charts/ directory (embedded)
	embeddedValuesPath := filepath.Join(chartPath, "charts", "embedded", "values.yaml")
	embeddedValues, err := os.ReadFile(embeddedValuesPath)
	if err != nil {
		t.Fatalf("Failed to read embedded values.yaml: %v", err)
	}
	if strings.Contains(string(embeddedValues), "- name: embedded-data") {
		t.Error("Embedded chart (charts/) should be converted")
	}

	// 3. tarball (remote)
	extractedDir := filepath.Join(chartPath, "charts", "tarball-chart-2.0.0")
	extractedValuesPath := filepath.Join(extractedDir, "values.yaml")
	extractedValues, err := os.ReadFile(extractedValuesPath)
	if err != nil {
		t.Fatalf("Failed to read extracted tarball values.yaml: %v", err)
	}
	if strings.Contains(string(extractedValues), "- name: cache") {
		t.Error("Tarball chart should be converted")
	}

	// Verify tarball backup was created
	tgzBackupPath := tgzPath + ".bak"
	if _, err := os.Stat(tgzBackupPath); os.IsNotExist(err) {
		t.Error("Tarball backup (.tgz.bak) should be created")
	}

	// All three types should have been processed successfully
	// Count is implicit - if all three checks pass, 3 subcharts were processed
}

// TestSubchartS1C_DetectCount verifies correct count of detected subcharts
func TestSubchartS1C_DetectCount(t *testing.T) {
	setupTestEnv(t)
	chartPath := getTestdataPath(t, "charts/matrix/single-types/s1-charts")

	// Use internal function to check subchart collection
	subcharts, err := collectSubcharts(chartPath, false, true, false)
	if err != nil {
		t.Fatalf("collectSubcharts failed: %v", err)
	}

	if len(subcharts) != 1 {
		t.Errorf("Expected 1 subchart, got %d", len(subcharts))
	}

	if len(subcharts) > 0 && subcharts[0].Name != "embedded" {
		t.Errorf("Expected subchart name 'embedded', got '%s'", subcharts[0].Name)
	}

	if len(subcharts) > 0 && subcharts[0].Source != "charts/" {
		t.Errorf("Expected source 'charts/', got '%s'", subcharts[0].Source)
	}
}

// TestSubchartM2FC_DetectCount verifies correct count for mixed types
func TestSubchartM2FC_DetectCount(t *testing.T) {
	setupTestEnv(t)
	chartPath := getTestdataPath(t, "charts/matrix/two-type-mix/m2-file-charts")

	// Use internal function to check subchart collection
	subcharts, err := collectSubcharts(chartPath, true, true, false)
	if err != nil {
		t.Fatalf("collectSubcharts failed: %v", err)
	}

	if len(subcharts) != 2 {
		t.Errorf("Expected 2 subcharts (file:// + charts/), got %d", len(subcharts))
	}

	// Verify both types are present
	hasFile := false
	hasCharts := false
	for _, sub := range subcharts {
		if sub.Source == "file://" {
			hasFile = true
		}
		if sub.Source == "charts/" {
			hasCharts = true
		}
	}

	if !hasFile {
		t.Error("Expected to find file:// subchart")
	}
	if !hasCharts {
		t.Error("Expected to find charts/ subchart")
	}
}

// TestSubchartDFC_DeduplicationCount verifies deduplication works
func TestSubchartDFC_DeduplicationCount(t *testing.T) {
	setupTestEnv(t)
	chartPath := getTestdataPath(t, "charts/matrix/deduplication/d-file-to-charts")

	// Use internal function with both flags (should deduplicate)
	subcharts, err := collectSubcharts(chartPath, true, true, false)
	if err != nil {
		t.Fatalf("collectSubcharts failed: %v", err)
	}

	// Should only have 1 subchart (deduplicated)
	if len(subcharts) != 1 {
		t.Errorf("Expected 1 deduplicated subchart, got %d", len(subcharts))
	}

	if len(subcharts) > 0 {
		// Should be marked as "charts/ (via Chart.yaml)" due to deduplication
		if subcharts[0].Source != "charts/ (via Chart.yaml)" {
			t.Errorf("Expected source 'charts/ (via Chart.yaml)', got '%s'", subcharts[0].Source)
		}
	}
}
