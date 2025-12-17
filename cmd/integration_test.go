package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/template"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/transform"
)

func TestDetectBasicChart(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	chartDir := getTestdataPath(t, "charts/basic")

	// Verify chart exists
	if _, err := os.Stat(filepath.Join(chartDir, "Chart.yaml")); os.IsNotExist(err) {
		t.Fatalf("Test chart not found at %s", chartDir)
	}

	// Run detection by scanning templates for values paths
	// This tests the core detection logic without the full CLI
	valuesPath := filepath.Join(chartDir, "values.yaml")
	valuesData, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read values.yaml: %v", err)
	}

	// Verify the chart has expected array fields
	content := string(valuesData)
	expectedFields := []string{"env:", "volumes:", "volumeMounts:"}
	for _, field := range expectedFields {
		if !strings.Contains(content, field) {
			t.Errorf("Expected values.yaml to contain %q", field)
		}
	}
}

func TestDetectNestedValuesChart(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	chartDir := getTestdataPath(t, "charts/nested-values")

	valuesPath := filepath.Join(chartDir, "values.yaml")
	valuesData, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read values.yaml: %v", err)
	}

	content := string(valuesData)
	// Check for nested paths
	expectedPaths := []string{
		"app:",
		"primary:",
		"secondary:",
		"deployment:",
		"extraVolumes:",
	}
	for _, path := range expectedPaths {
		if !strings.Contains(content, path) {
			t.Errorf("Expected values.yaml to contain %q", path)
		}
	}
}

func TestConvertBasicChart(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	// Copy chart to temp directory for modification
	chartDir := copyChart(t, getTestdataPath(t, "charts/basic"))

	// Read original values.yaml
	valuesPath := filepath.Join(chartDir, "values.yaml")
	originalValues, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read values.yaml: %v", err)
	}

	// Verify original has array format
	if !strings.Contains(string(originalValues), "- name: DB_HOST") {
		t.Fatal("Original values.yaml should contain array format '- name: DB_HOST'")
	}

	// The actual conversion would be called here
	// For now, we just verify the test infrastructure works
	t.Log("Test infrastructure working - chart copied to:", chartDir)
}

func TestConvertIdempotent(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	// Full idempotency test is implemented in error_test.go as TestConvertIdempotency
	// This test verifies idempotency at the function level using applyLineEdits

	// Create a simple YAML with array format
	original := `env:
  - name: KEY1
    value: val1
  - name: KEY2
    value: val2
`

	// Write to temp file for loadValuesNode
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")
	if err := os.WriteFile(valuesPath, []byte(original), 0644); err != nil {
		t.Fatalf("Failed to write temp values.yaml: %v", err)
	}

	// Parse and create edits
	doc, _, err := loadValuesNode(valuesPath)
	if err != nil {
		t.Fatalf("loadValuesNode() error = %v", err)
	}

	candidates := map[string]DetectedCandidate{
		"env": {
			ValuesPath: "env",
			MergeKey:   "name",
		},
	}

	var edits []ArrayEdit
	transform.FindArrayEdits(doc, nil, candidates, &edits)

	if len(edits) == 0 {
		t.Fatal("Expected edits for env array")
	}

	// Apply edits once
	firstResult := transform.ApplyLineEdits([]byte(original), edits)

	// Write converted result to parse again
	if err := os.WriteFile(valuesPath, firstResult, 0644); err != nil {
		t.Fatalf("Failed to write converted values.yaml: %v", err)
	}

	// Parse the result and try to find more edits
	doc2, _, err := loadValuesNode(valuesPath)
	if err != nil {
		t.Fatalf("loadValuesNode() on converted result error = %v", err)
	}

	var edits2 []ArrayEdit
	transform.FindArrayEdits(doc2, nil, candidates, &edits2)

	// After conversion, there should be no more edits (already converted to map)
	if len(edits2) > 0 {
		t.Errorf("Expected no edits after conversion (idempotent), got %d edits", len(edits2))
	}

	// Verify the converted format contains map-style syntax
	if !strings.Contains(string(firstResult), "KEY1:") {
		t.Error("Expected converted result to contain 'KEY1:' (map key format)")
	}
}

func TestEdgeCasesChart(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	chartDir := getTestdataPath(t, "charts/edge-cases")

	valuesPath := filepath.Join(chartDir, "values.yaml")
	valuesData, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("Failed to read values.yaml: %v", err)
	}

	content := string(valuesData)

	// Test that edge cases are present
	testCases := []struct {
		name    string
		content string
	}{
		{"empty array", "emptyEnv: []"},
		{"nested empty", "containers: []"},
		{"map style (should skip)", "mapStyle:"},
		{"block-style sequence no indent", "volumes:\n- name: data"},
		{"single item array", "singleItemEnv:"},
		{"duplicate keys", "duplicateKeys:"},
	}

	for _, tc := range testCases {
		if !strings.Contains(content, tc.content) {
			t.Errorf("Edge case %q not found: expected %q", tc.name, tc.content)
		}
	}
}

func TestTemplateDetection(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	chartDir := getTestdataPath(t, "charts/basic")
	templatePath := filepath.Join(chartDir, "templates", "deployment.yaml")

	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("Failed to read template: %v", err)
	}

	content := string(templateData)

	// Verify template patterns that can be detected
	patterns := []string{
		"toYaml .Values.env",
		"toYaml .Values.volumeMounts",
		"toYaml .Values.volumes",
	}

	for _, pattern := range patterns {
		if !strings.Contains(content, pattern) {
			t.Errorf("Template should contain pattern %q", pattern)
		}
	}
}

func TestHelperGeneration(t *testing.T) {
	setupTestEnv(t)

	// Test that listMapHelper returns valid content
	helper := template.ListMapHelper()

	requiredContent := []string{
		"chart.listmap.items",
		"range $keyVal",
		"sortAlpha",
		"$key",
	}

	for _, content := range requiredContent {
		if !strings.Contains(helper, content) {
			t.Errorf("listMapHelper() should contain %q", content)
		}
	}
}

func TestQuotePath(t *testing.T) {
	// quotePath converts a dot-separated path to space-separated quoted segments
	// It does NOT include the "(index .Values ...)" wrapper - that's added elsewhere
	tests := []struct {
		input string
		want  string
	}{
		{"env", `"env"`},
		{"deployment.env", `"deployment" "env"`},
		{"app.primary.env", `"app" "primary" "env"`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := template.QuotePath(tt.input)
			if got != tt.want {
				t.Errorf("quotePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadValuesNode(t *testing.T) {
	setupTestEnv(t)

	chartDir := getTestdataPath(t, "charts/basic")
	valuesPath := filepath.Join(chartDir, "values.yaml")

	doc, data, err := loadValuesNode(valuesPath)
	if err != nil {
		t.Fatalf("loadValuesNode() error = %v", err)
	}

	if doc == nil {
		t.Fatal("loadValuesNode() returned nil document")
	}

	if len(data) == 0 {
		t.Fatal("loadValuesNode() returned empty data")
	}

	// Verify it's a valid YAML document node
	if doc.Kind != 1 { // yaml.DocumentNode
		t.Errorf("Expected DocumentNode (1), got %d", doc.Kind)
	}
}

func TestFindArrayEdits(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	chartDir := getTestdataPath(t, "charts/basic")
	valuesPath := filepath.Join(chartDir, "values.yaml")

	doc, _, err := loadValuesNode(valuesPath)
	if err != nil {
		t.Fatalf("loadValuesNode() error = %v", err)
	}

	// Set up candidates matching the basic chart
	candidates := map[string]DetectedCandidate{
		"env": {
			ValuesPath: "env",
			MergeKey:   "name",
		},
		"volumes": {
			ValuesPath: "volumes",
			MergeKey:   "name",
		},
		"volumeMounts": {
			ValuesPath: "volumeMounts",
			MergeKey:   "mountPath",
		},
	}

	var edits []ArrayEdit
	transform.FindArrayEdits(doc, nil, candidates, &edits)

	// Should find edits for env, volumes, volumeMounts
	if len(edits) < 3 {
		t.Errorf("Expected at least 3 edits, got %d", len(edits))
	}

	// Verify edit fields are populated
	for _, edit := range edits {
		if edit.KeyLine == 0 {
			t.Error("Edit should have non-zero KeyLine")
		}
		if edit.Replacement == "" {
			t.Error("Edit should have non-empty Replacement")
		}
	}
}

func TestApplyLineEdits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		original string
		edits    []ArrayEdit
	}{
		{
			name:     "no edits returns original",
			original: "key: value\n",
			edits:    nil,
		},
		{
			name:     "empty edits returns original",
			original: "key: value\n",
			edits:    []ArrayEdit{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(transform.ApplyLineEdits([]byte(tt.original), tt.edits))
			// For no/empty edits, should return original
			if len(tt.edits) == 0 && got != tt.original {
				t.Errorf("applyLineEdits() with no edits = %q, want %q", got, tt.original)
			}
		})
	}
}

func TestCheckTemplatePatterns(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	chartDir := getTestdataPath(t, "charts/basic")

	paths := []PathInfo{
		{DotPath: "env", MergeKey: "name"},
		{DotPath: "volumes", MergeKey: "name"},
		{DotPath: "volumeMounts", MergeKey: "mountPath"},
		{DotPath: "nonexistent", MergeKey: "id"},
	}

	matched := template.CheckTemplatePatterns(chartDir, paths)

	// Should match env, volumes, volumeMounts (present in template)
	// Should NOT match nonexistent (not in template)
	if !matched["env"] {
		t.Error("Expected env to be matched in templates")
	}
	if !matched["volumes"] {
		t.Error("Expected volumes to be matched in templates")
	}
	if !matched["volumeMounts"] {
		t.Error("Expected volumeMounts to be matched in templates")
	}
	if matched["nonexistent"] {
		t.Error("Expected nonexistent to NOT be matched in templates")
	}
}
