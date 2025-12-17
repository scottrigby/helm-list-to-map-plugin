package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommentedExampleStripping tests the removal of commented-out YAML examples
// after array conversion. These tests use fixtures in testdata/values/.

func TestCommentedExampleEmptyArray(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	// Read the fixture file
	fixturePath := getTestdataPath(t, "values/commented-example-empty-array.yaml")
	original, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	// Write to temp file for processing
	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")
	if err := os.WriteFile(valuesPath, original, 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	// Parse and find edits
	doc, _, err := loadValuesNode(valuesPath)
	if err != nil {
		t.Fatalf("loadValuesNode() error = %v", err)
	}

	// Set up candidates for fields we want to convert
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
		"labels": {
			ValuesPath: "labels",
			MergeKey:   "name",
		},
	}

	var edits []ArrayEdit
	findArrayEdits(doc, nil, candidates, &edits)

	// Apply edits
	result := applyLineEdits(original, edits)
	resultStr := string(result)

	// Verify: Commented examples after empty arrays should be removed
	tests := []struct {
		name        string
		shouldExist bool
		content     string
	}{
		// These commented examples should be REMOVED
		{"commented DATABASE_URL example", false, "# - name: DATABASE_URL"},
		{"commented data volume example", false, "# - name: data\n"}, // Distinct from volumeMounts
		{"commented config volume example", false, "# - name: config"},
		{"commented mountPath example", false, "#   mountPath: /data"},

		// Control: Empty ports array with no comments should convert to {}
		{"ports converted to map", true, "ports:"},

		// Control: Non-empty labels array should be converted (not stripped)
		{"labels app entry", true, "app:"},
		{"labels version entry", true, "version:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists := strings.Contains(resultStr, tt.content)
			if tt.shouldExist && !exists {
				t.Errorf("Expected result to contain %q but it doesn't.\nResult:\n%s", tt.content, resultStr)
			}
			if !tt.shouldExist && exists {
				t.Errorf("Expected result to NOT contain %q but it does.\nResult:\n%s", tt.content, resultStr)
			}
		})
	}
}

func TestCommentedExampleMultiLine(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	fixturePath := getTestdataPath(t, "values/commented-example-multi-line.yaml")
	original, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")
	if err := os.WriteFile(valuesPath, original, 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	doc, _, err := loadValuesNode(valuesPath)
	if err != nil {
		t.Fatalf("loadValuesNode() error = %v", err)
	}

	candidates := map[string]DetectedCandidate{
		"initContainers": {
			ValuesPath: "initContainers",
			MergeKey:   "name",
		},
		"sidecars": {
			ValuesPath: "sidecars",
			MergeKey:   "name",
		},
		"extraEnv": {
			ValuesPath: "extraEnv",
			MergeKey:   "name",
		},
	}

	var edits []ArrayEdit
	findArrayEdits(doc, nil, candidates, &edits)
	result := applyLineEdits(original, edits)
	resultStr := string(result)

	tests := []struct {
		name        string
		shouldExist bool
		content     string
	}{
		// Multi-line commented examples should be fully removed
		{"commented init-db example", false, "# - name: init-db"},
		{"commented init-db image", false, "#   image: postgres:15"},
		{"commented init-db command", false, "#     - sh"},

		// Multiple commented items should all be removed
		{"commented logging-sidecar", false, "# - name: logging-sidecar"},
		{"commented metrics-sidecar", false, "# - name: metrics-sidecar"},

		// Trailing comments after real array should be stripped
		{"commented FEATURE_FLAG", false, "# - name: FEATURE_FLAG"},
		{"commented EXPERIMENTAL", false, "# - name: EXPERIMENTAL"},

		// Real converted entries should exist
		{"extraEnv LOG_LEVEL converted", true, "LOG_LEVEL:"},
		{"extraEnv DEBUG converted", true, "DEBUG:"},

		// Descriptive comments (not array examples) should be preserved
		{"descriptive comment", true, "# These are the resource limits"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists := strings.Contains(resultStr, tt.content)
			if tt.shouldExist && !exists {
				t.Errorf("Expected result to contain %q but it doesn't.\nResult:\n%s", tt.content, resultStr)
			}
			if !tt.shouldExist && exists {
				t.Errorf("Expected result to NOT contain %q but it does.\nResult:\n%s", tt.content, resultStr)
			}
		})
	}
}

func TestCommentedExampleNested(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	fixturePath := getTestdataPath(t, "values/commented-example-nested.yaml")
	original, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}

	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")
	if err := os.WriteFile(valuesPath, original, 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	doc, _, err := loadValuesNode(valuesPath)
	if err != nil {
		t.Fatalf("loadValuesNode() error = %v", err)
	}

	// Candidates for nested paths
	candidates := map[string]DetectedCandidate{
		"database.primary.env": {
			ValuesPath: "database.primary.env",
			MergeKey:   "name",
		},
		"database.replica.env": {
			ValuesPath: "database.replica.env",
			MergeKey:   "name",
		},
		"app.frontend.deployment.containers": {
			ValuesPath: "app.frontend.deployment.containers",
			MergeKey:   "name",
		},
		"app.backend.deployment.initContainers": {
			ValuesPath: "app.backend.deployment.initContainers",
			MergeKey:   "name",
		},
		"monitoring.annotations": {
			ValuesPath: "monitoring.annotations",
			MergeKey:   "name",
		},
	}

	var edits []ArrayEdit
	findArrayEdits(doc, nil, candidates, &edits)
	result := applyLineEdits(original, edits)
	resultStr := string(result)

	tests := []struct {
		name        string
		shouldExist bool
		content     string
	}{
		// Commented examples at nested paths should be removed
		{"commented POSTGRES_USER in primary.env", false, "# - name: POSTGRES_USER"},
		{"commented POSTGRES_PASSWORD secretKeyRef", false, "#     secretKeyRef:"},

		// Comments after real array in replica.env should be stripped
		{"commented password in replica", false, "# - name: POSTGRES_PASSWORD"},

		// Real converted entry should exist
		{"replica POSTGRES_USER converted", true, "POSTGRES_USER:"},
		{"replica readonly value", true, "value: readonly"},

		// Deeply nested real content should be converted
		{"frontend nginx container", true, "nginx:"},

		// Section separator comments should be preserved
		{"section separator comment", true, "# This comment separates logical sections"},

		// Descriptive comments should be preserved
		{"descriptive comment for initContainers", true, "# It explains what the initContainers do"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists := strings.Contains(resultStr, tt.content)
			if tt.shouldExist && !exists {
				t.Errorf("Expected result to contain %q but it doesn't.\nResult:\n%s", tt.content, resultStr)
			}
			if !tt.shouldExist && exists {
				t.Errorf("Expected result to NOT contain %q but it does.\nResult:\n%s", tt.content, resultStr)
			}
		})
	}
}

// TestCommentStrippingPreservesStructure verifies that comment stripping
// doesn't break the overall YAML structure
func TestCommentStrippingPreservesStructure(t *testing.T) {
	setupTestEnv(t)
	resetGlobalState(t)

	// Simple inline test for structure preservation
	original := `# Header comment
app:
  env: []
    # - name: FOO
    #   value: bar

  # This is a section comment that should stay
  config:
    key: value
`

	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "values.yaml")
	if err := os.WriteFile(valuesPath, []byte(original), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	doc, _, err := loadValuesNode(valuesPath)
	if err != nil {
		t.Fatalf("loadValuesNode() error = %v", err)
	}

	candidates := map[string]DetectedCandidate{
		"app.env": {
			ValuesPath: "app.env",
			MergeKey:   "name",
		},
	}

	var edits []ArrayEdit
	findArrayEdits(doc, nil, candidates, &edits)
	result := applyLineEdits([]byte(original), edits)
	resultStr := string(result)

	// Verify structure is preserved
	if !strings.Contains(resultStr, "# Header comment") {
		t.Error("Header comment should be preserved")
	}
	if !strings.Contains(resultStr, "# This is a section comment") {
		t.Error("Section comment should be preserved")
	}
	if !strings.Contains(resultStr, "config:") {
		t.Error("config section should be preserved")
	}
	if !strings.Contains(resultStr, "key: value") {
		t.Error("config content should be preserved")
	}

	// Verify commented examples are removed
	if strings.Contains(resultStr, "# - name: FOO") {
		t.Error("Commented array example should be removed")
	}
}
