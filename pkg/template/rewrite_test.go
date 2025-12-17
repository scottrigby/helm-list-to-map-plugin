package template

import (
	"strings"
	"testing"
)

func TestReplaceListBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		dotPath  string
		mergeKey string
		want     string
		changed  bool
	}{
		{
			name:     "pattern 1: toYaml with nindent",
			template: `{{- toYaml .Values.env | nindent 12 }}`,
			dotPath:  "env",
			mergeKey: "name",
			want:     `{{- include "chart.listmap.items" (dict "items" (index .Values "env") "key" "name") | nindent 12 }}`,
			changed:  true,
		},
		{
			name:     "pattern 1: toYaml without dash",
			template: `{{ toYaml .Values.env | nindent 8 }}`,
			dotPath:  "env",
			mergeKey: "name",
			want:     `{{- include "chart.listmap.items" (dict "items" (index .Values "env") "key" "name") | nindent 8 }}`,
			changed:  true,
		},
		{
			name:     "pattern 1: nested path",
			template: `{{- toYaml .Values.deployment.env | nindent 12 }}`,
			dotPath:  "deployment.env",
			mergeKey: "name",
			want:     `{{- include "chart.listmap.items" (dict "items" (index .Values "deployment" "env") "key" "name") | nindent 12 }}`,
			changed:  true,
		},
		{
			name:     "pattern 2: toYaml with indent (no n prefix)",
			template: `{{ toYaml .Values.volumes | indent 8 }}`,
			dotPath:  "volumes",
			mergeKey: "name",
			want:     `{{- include "chart.listmap.items" (dict "items" (index .Values "volumes") "key" "name") | nindent 8 }}`,
			changed:  true,
		},
		{
			name:     "pattern 5: range loop",
			template: "spec:\n  volumes:\n    {{- range .Values.volumes }}\n    - name: {{ .name }}\n    {{- end }}",
			dotPath:  "volumes",
			mergeKey: "name",
			// Range pattern gets replaced with if block
			want:    "chart.listmap.items",
			changed: true,
		},
		{
			name:     "no match - different path",
			template: `{{- toYaml .Values.other | nindent 12 }}`,
			dotPath:  "env",
			mergeKey: "name",
			want:     `{{- toYaml .Values.other | nindent 12 }}`,
			changed:  false,
		},
		{
			name:     "no match - not a toYaml call",
			template: `{{- .Values.env }}`,
			dotPath:  "env",
			mergeKey: "name",
			want:     `{{- .Values.env }}`,
			changed:  false,
		},
		{
			name:     "volumeMounts with mountPath key",
			template: `{{- toYaml .Values.volumeMounts | nindent 12 }}`,
			dotPath:  "volumeMounts",
			mergeKey: "mountPath",
			want:     `{{- include "chart.listmap.items" (dict "items" (index .Values "volumeMounts") "key" "mountPath") | nindent 12 }}`,
			changed:  true,
		},
		{
			name:     "ports with containerPort key",
			template: `{{- toYaml .Values.ports | nindent 12 }}`,
			dotPath:  "ports",
			mergeKey: "containerPort",
			want:     `{{- include "chart.listmap.items" (dict "items" (index .Values "ports") "key" "containerPort") | nindent 12 }}`,
			changed:  true,
		},
		{
			name:     "deeply nested path",
			template: `{{- toYaml .Values.app.primary.env | nindent 16 }}`,
			dotPath:  "app.primary.env",
			mergeKey: "name",
			want:     `{{- include "chart.listmap.items" (dict "items" (index .Values "app" "primary" "env") "key" "name") | nindent 16 }}`,
			changed:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := ReplaceListBlocks(tt.template, tt.dotPath, tt.mergeKey, "")
			if changed != tt.changed {
				t.Errorf("ReplaceListBlocks() changed = %v, want %v", changed, tt.changed)
			}
			if tt.changed {
				// For changed templates, verify the expected content is present
				if !strings.Contains(got, tt.want) && got != tt.want {
					t.Errorf("ReplaceListBlocks() got = %q\nwant to contain: %q", got, tt.want)
				}
			} else {
				// For unchanged templates, verify exact match
				if got != tt.want {
					t.Errorf("ReplaceListBlocks() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestReplaceListBlocksWithContext(t *testing.T) {
	// Test pattern 3: with block pattern
	template := `{{- with .Values.env }}
env:
  {{- toYaml . | nindent 12 }}
{{- end }}`

	got, changed := ReplaceListBlocks(template, "env", "name", "")
	if !changed {
		t.Error("Expected template to be changed")
	}
	if !strings.Contains(got, "chart.listmap.items") {
		t.Errorf("Expected transformed template to contain helper call, got: %s", got)
	}
}

func TestReplaceListBlocksPreservesUnrelated(t *testing.T) {
	// Template with multiple sections, only one should be modified
	template := `spec:
  containers:
    - name: app
      env:
        {{- toYaml .Values.env | nindent 12 }}
      volumeMounts:
        {{- toYaml .Values.volumeMounts | nindent 12 }}`

	// Only replace env
	got, changed := ReplaceListBlocks(template, "env", "name", "")
	if !changed {
		t.Error("Expected template to be changed")
	}

	// Verify env was replaced
	if strings.Contains(got, "toYaml .Values.env") {
		t.Error("Expected .Values.env to be replaced")
	}

	// Verify volumeMounts was NOT replaced
	if !strings.Contains(got, "toYaml .Values.volumeMounts") {
		t.Error("Expected .Values.volumeMounts to NOT be replaced")
	}
}

func TestListMapHelperContent(t *testing.T) {
	helper := ListMapHelper()

	// Verify it's a valid Go template definition
	if !strings.Contains(helper, "{{- define") {
		t.Error("Helper should be a template definition")
	}

	// Verify it uses range to iterate
	if !strings.Contains(helper, "range") {
		t.Error("Helper should use range to iterate")
	}

	// Verify it uses sortAlpha for deterministic ordering
	if !strings.Contains(helper, "sortAlpha") {
		t.Error("Helper should use sortAlpha for deterministic ordering")
	}

	// Verify it references the key parameter
	if !strings.Contains(helper, "$key") {
		t.Error("Helper should reference $key parameter")
	}

	// Verify it uses toYaml for the spec
	if !strings.Contains(helper, "toYaml $spec") {
		t.Error("Helper should use toYaml for spec values")
	}
}

func TestQuotePathEdgeCases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", `"simple"`},
		{"two.parts", `"two" "parts"`},
		{"a.b.c.d", `"a" "b" "c" "d"`},
		// Edge cases
		{"", `""`}, // Empty string becomes single empty quoted string
		{"a", `"a"`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := quotePath(tt.input)
			if got != tt.want {
				t.Errorf("quotePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestDotPathJoining removed - dotPath is now internal to pkg/transform
