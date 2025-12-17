//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/integration/testutil"
	internaltestutil "github.com/scottrigby/helm-list-to-map-plugin/internal/testutil"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/k8s"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/transform"
	"gopkg.in/yaml.v3"
)

// loadValuesNode loads a values.yaml file and returns the parsed YAML node
func loadValuesNode(path string) (*yaml.Node, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}

	return &doc, data, nil
}

// TestCommentedExampleEmptyArray tests the removal of commented-out YAML examples
// after array conversion.
func TestCommentedExampleEmptyArray(t *testing.T) {
	internaltestutil.SetupTestEnv(t)
	internaltestutil.ResetGlobalState(t)

	// Read the fixture file
	fixturePath := testutil.GetTestdataPath(t, "values/commented-example-empty-array.yaml")
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
	candidates := map[string]k8s.DetectedCandidate{
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

	var edits []transform.ArrayEdit
	transform.FindArrayEdits(doc, nil, candidates, &edits)

	// Apply edits
	result := transform.ApplyLineEdits(original, edits)
	resultStr := string(result)

	// Verify: Commented examples after empty arrays should be removed
	tests := []struct {
		name        string
		shouldExist bool
		content     string
	}{
		// These commented examples should be REMOVED
		{"commented DATABASE_URL example", false, "# - name: DATABASE_URL"},
		{"commented data volume example", false, "# - name: data\n"},
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

// TestCommentStrippingPreservesStructure verifies that comment stripping
// doesn't break the overall YAML structure
func TestCommentStrippingPreservesStructure(t *testing.T) {
	internaltestutil.SetupTestEnv(t)
	internaltestutil.ResetGlobalState(t)

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

	candidates := map[string]k8s.DetectedCandidate{
		"app.env": {
			ValuesPath: "app.env",
			MergeKey:   "name",
		},
	}

	var edits []transform.ArrayEdit
	transform.FindArrayEdits(doc, nil, candidates, &edits)
	result := transform.ApplyLineEdits([]byte(original), edits)
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
