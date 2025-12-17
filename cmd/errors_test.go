package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/internal/testutil"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/crd"
	pkgfs "github.com/scottrigby/helm-list-to-map-plugin/pkg/fs"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/transform"
)

// TestDetectNoChart tests detect without valid chart directory
func TestDetectNoChart(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	// Empty chart path
	err := runDetect(DetectOptions{
		ChartDir: "",
	})

	if err == nil {
		t.Error("expected error when chart path is empty")
	}
}

// TestDetectNonexistentChart tests detect with a chart that doesn't exist
func TestDetectNonexistentChart(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	err := runDetect(DetectOptions{
		ChartDir: "/nonexistent/path/to/chart",
	})

	if err == nil {
		t.Error("expected error for nonexistent chart path")
	}

	if !strings.Contains(err.Error(), "no such file") &&
		!strings.Contains(err.Error(), "not found") &&
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should indicate path doesn't exist, got: %v", err)
	}
}

// TestConvertNoChart tests convert without valid chart directory
func TestConvertNoChart(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	// Empty chart path
	err := runConvert(ConvertOptions{
		ChartDir:  "",
		BackupExt: ".bak",
	})

	if err == nil {
		t.Error("expected error when chart path is empty")
	}
}

// TestConvertNonexistentChart tests convert with a chart that doesn't exist
func TestConvertNonexistentChart(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	err := runConvert(ConvertOptions{
		ChartDir:  "/nonexistent/path/to/chart",
		BackupExt: ".bak",
	})

	if err == nil {
		t.Error("expected error for nonexistent chart path")
	}

	if !strings.Contains(err.Error(), "no such file") &&
		!strings.Contains(err.Error(), "not found") &&
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should indicate path doesn't exist, got: %v", err)
	}
}

// TestChartWithNoValuesYaml tests behavior when a chart has no values.yaml
func TestChartWithNoValuesYaml(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

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

	// Should not crash, may show no candidates found
	output, err := captureOutput(t, func() error {
		return runDetect(DetectOptions{
			ChartDir: tmpDir,
		})
	})

	// May succeed or fail, but should not panic
	_ = err
	t.Logf("detect with no values.yaml: %s", output)
}

// TestChartWithEmptyValuesYaml tests behavior when values.yaml is empty
func TestChartWithEmptyValuesYaml(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

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

	// Should not crash, may show no candidates found
	output, err := captureOutput(t, func() error {
		return runDetect(DetectOptions{
			ChartDir: tmpDir,
		})
	})

	// May succeed or fail, but should not panic
	_ = err
	t.Logf("detect with empty values.yaml: %s", output)
}

// TestConvertIdempotency tests that running convert twice produces same result
func TestConvertIdempotency(t *testing.T) {
	testutil.SetupTestEnv(t)
	testutil.ResetGlobalState(t)

	chartPath := copyChartForTest(t, "testdata/charts/basic")

	// First conversion
	_, err := captureOutput(t, func() error {
		return runConvert(ConvertOptions{
			ChartDir:  chartPath,
			BackupExt: ".bak",
		})
	})
	if err != nil {
		t.Fatalf("first convert failed: %v", err)
	}

	// Read state after first conversion
	valuesAfterFirst, err := os.ReadFile(filepath.Join(chartPath, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// Second conversion
	_, err = captureOutput(t, func() error {
		return runConvert(ConvertOptions{
			ChartDir:  chartPath,
			BackupExt: ".bak",
		})
	})
	if err != nil {
		t.Fatalf("second convert failed: %v", err)
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

// TestCRDRegistryLoadFromFileErrors tests CRD loading error cases
func TestCRDRegistryLoadFromFileErrors(t *testing.T) {
	t.Parallel()

	// Test loading from non-existent file
	reg := crd.NewCRDRegistry(pkgfs.OSFileSystem{})
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

	reg := crd.NewCRDRegistry(pkgfs.OSFileSystem{})

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
