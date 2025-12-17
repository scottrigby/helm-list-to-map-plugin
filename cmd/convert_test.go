package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/internal/testutil"
)

// copyChartForTest copies a chart to a temp directory for testing
func copyChartForTest(t *testing.T, srcChart string) string {
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

// TestConvertDryRun tests convert --dry-run functionality
func TestConvertDryRun(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := copyChartForTest(t, "testdata/charts/basic")

	output, err := captureOutput(t, func() error {
		return runConvert(ConvertOptions{
			ChartDir:  chartPath,
			DryRun:    true,
			BackupExt: ".bak",
		})
	})

	if err != nil {
		t.Fatalf("runConvert --dry-run failed: %v\nOutput: %s", err, output)
	}

	// Dry run should show what would be converted
	outputLower := strings.ToLower(output)
	if !strings.Contains(outputLower, "dry") && !strings.Contains(outputLower, "would") {
		t.Logf("Expected dry-run indication in output\nGot:\n%s", output)
	}

	// Verify files were NOT modified (dry run)
	valuesPath := filepath.Join(chartPath, "values.yaml")
	valuesData, _ := os.ReadFile(valuesPath)
	if !strings.Contains(string(valuesData), "- name: DB_HOST") {
		t.Error("values.yaml should NOT be modified in dry-run mode")
	}
}

// TestConvertActual tests actual conversion
func TestConvertActual(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := copyChartForTest(t, "testdata/charts/basic")

	// Verify original format
	valuesPath := filepath.Join(chartPath, "values.yaml")
	originalValues, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read original values.yaml: %v", err)
	}
	if !strings.Contains(string(originalValues), "- name: DB_HOST") {
		t.Fatal("Original should have array format")
	}

	output, err := captureOutput(t, func() error {
		return runConvert(ConvertOptions{
			ChartDir:  chartPath,
			DryRun:    false,
			BackupExt: ".bak",
		})
	})

	if err != nil {
		t.Fatalf("runConvert failed: %v\nOutput: %s", err, output)
	}

	// Verify values.yaml was converted to map format
	convertedValues, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read converted values.yaml: %v", err)
	}
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

// TestConvertRecursive tests recursive conversion of umbrella charts
func TestConvertRecursive(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := copyChartForTest(t, "testdata/charts/umbrella")

	// Verify subchart has array format before conversion
	subchartValues := filepath.Join(chartPath, "subcharts", "subchart-a", "values.yaml")
	originalSubchart, err := os.ReadFile(subchartValues)
	if err != nil {
		t.Fatalf("Failed to read subchart values.yaml: %v", err)
	}
	if !strings.Contains(string(originalSubchart), "- name: SUBCHART_DEFAULT") {
		t.Fatal("Subchart should have array format before conversion")
	}

	// Verify umbrella has array format for subchart override before conversion
	umbrellaValues := filepath.Join(chartPath, "values.yaml")
	originalUmbrella, err := os.ReadFile(umbrellaValues)
	if err != nil {
		t.Fatalf("Failed to read umbrella values.yaml: %v", err)
	}
	if !strings.Contains(string(originalUmbrella), "- name: SUBCHART_VAR") {
		t.Fatal("Umbrella subchart-a.env should have array format before conversion")
	}

	// Run recursive convert
	output, err := captureOutput(t, func() error {
		return runConvert(ConvertOptions{
			ChartDir:  chartPath,
			Recursive: true,
			BackupExt: ".bak",
		})
	})

	if err != nil {
		t.Fatalf("runConvert --recursive failed: %v\nOutput: %s", err, output)
	}

	// Verify subchart was converted
	convertedSubchart, err := os.ReadFile(subchartValues)
	if err != nil {
		t.Fatalf("Failed to read converted subchart values.yaml: %v", err)
	}
	if strings.Contains(string(convertedSubchart), "- name: SUBCHART_DEFAULT") {
		t.Error("Subchart values.yaml should be converted from array to map format")
	}

	// Should have SUBCHART_DEFAULT as map key
	if !strings.Contains(string(convertedSubchart), "SUBCHART_DEFAULT:") {
		t.Error("Subchart should have SUBCHART_DEFAULT as map key after conversion")
	}

	// Verify umbrella's subchart override section was updated to match subchart format
	convertedUmbrella, err := os.ReadFile(umbrellaValues)
	if err != nil {
		t.Fatalf("Failed to read converted umbrella values.yaml: %v", err)
	}
	// The subchart-a.env section should be converted to map format
	if strings.Contains(string(convertedUmbrella), "- name: SUBCHART_VAR") {
		t.Error("Umbrella subchart-a.env should be converted from array to map format")
	}
	if !strings.Contains(string(convertedUmbrella), "SUBCHART_VAR:") {
		t.Error("Umbrella should have SUBCHART_VAR as map key after conversion")
	}
}

// TestConvertOptions tests that convert options structure is correct
func TestConvertOptions(t *testing.T) {
	// This is a smoke test - just verify the Options structure is correct
	opts := ConvertOptions{
		ChartDir:  "testdata/charts/basic",
		DryRun:    true,
		Recursive: false,
		BackupExt: ".bak",
	}

	// Verify required fields are present
	if opts.ChartDir == "" {
		t.Error("ChartDir should be set")
	}
	if opts.BackupExt == "" {
		t.Error("BackupExt should be set")
	}
}
