package main

import (
	"strings"
	"testing"
)

func TestTransformArrayToMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arrayLines []string
		mergeKey   string
		want       []string
	}{
		{
			name: "simple env array",
			arrayLines: []string{
				"  - name: DB_HOST",
				"    value: localhost",
				"  - name: DB_PORT",
				"    value: \"5432\"",
			},
			mergeKey: "name",
			want: []string{
				"  DB_HOST:",
				"    value: localhost",
				"  DB_PORT:",
				"    value: \"5432\"",
			},
		},
		{
			name: "single item array",
			arrayLines: []string{
				"  - name: ONLY_VAR",
				"    value: only",
			},
			mergeKey: "name",
			want: []string{
				"  ONLY_VAR:",
				"    value: only",
			},
		},
		{
			name: "volume with nested structure",
			arrayLines: []string{
				"  - name: config",
				"    configMap:",
				"      name: my-config",
				"  - name: data",
				"    emptyDir: {}",
			},
			mergeKey: "name",
			want: []string{
				"  config:",
				"    configMap:",
				"      name: my-config",
				"  data:",
				"    emptyDir: {}",
			},
		},
		{
			name: "volumeMounts with mountPath key",
			arrayLines: []string{
				"  - name: config",
				"    mountPath: /etc/config",
				"  - name: data",
				"    mountPath: /data",
				"    readOnly: true",
			},
			mergeKey: "mountPath",
			want: []string{
				"  /etc/config:",
				"    name: config",
				"  /data:",
				"    name: data",
				"    readOnly: true",
			},
		},
		{
			name: "ports with containerPort key",
			arrayLines: []string{
				"  - containerPort: 8080",
				"    name: http",
				"    protocol: TCP",
				"  - containerPort: 8443",
				"    name: https",
			},
			mergeKey: "containerPort",
			want: []string{
				"  8080:",
				"    name: http",
				"    protocol: TCP",
				"  8443:",
				"    name: https",
			},
		},
		{
			name:       "empty array",
			arrayLines: []string{},
			mergeKey:   "name",
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transformArrayToMap(tt.arrayLines, tt.mergeKey)

			if len(got) != len(tt.want) {
				t.Errorf("transformArrayToMap() returned %d lines, want %d\nGot:\n%s\nWant:\n%s",
					len(got), len(tt.want),
					strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
				return
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Line %d mismatch:\nGot:  %q\nWant: %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTransformSingleItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		itemLines  []string
		mergeKey   string
		baseIndent string
		want       []string
	}{
		{
			name: "env var with value",
			itemLines: []string{
				"  - name: DB_HOST",
				"    value: localhost",
			},
			mergeKey:   "name",
			baseIndent: "  ",
			want: []string{
				"  DB_HOST:",
				"    value: localhost",
			},
		},
		{
			name: "volume with nested configMap",
			itemLines: []string{
				"  - name: config",
				"    configMap:",
				"      name: my-config",
				"      items:",
				"        - key: config.yaml",
				"          path: config.yaml",
			},
			mergeKey:   "name",
			baseIndent: "  ",
			want: []string{
				"  config:",
				"    configMap:",
				"      name: my-config",
				"      items:",
				"        - key: config.yaml",
				"          path: config.yaml",
			},
		},
		{
			name: "merge key with special characters",
			itemLines: []string{
				"  - mountPath: /etc/config",
				"    name: config",
			},
			mergeKey:   "mountPath",
			baseIndent: "  ",
			want: []string{
				"  /etc/config:",
				"    name: config",
			},
		},
		{
			name: "preserve inline comment",
			itemLines: []string{
				"  - name: DEBUG  # Enable debug mode",
				"    value: \"true\"",
			},
			mergeKey:   "name",
			baseIndent: "  ",
			want: []string{
				"  DEBUG: # Enable debug mode", // Comment spacing may be normalized
				"    value: \"true\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transformSingleItem(tt.itemLines, tt.mergeKey, tt.baseIndent)

			if len(got) != len(tt.want) {
				t.Errorf("transformSingleItem() returned %d lines, want %d\nGot:\n%s\nWant:\n%s",
					len(got), len(tt.want),
					strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
				return
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Line %d mismatch:\nGot:  %q\nWant: %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTransformSingleItemWithIndent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		itemLines      []string
		mergeKey       string
		baseIndent     string
		mapEntryIndent int
		want           []string
	}{
		{
			name: "block-style sequence with no indentation (minio style)",
			itemLines: []string{
				"- name: data",
				"  emptyDir: {}",
			},
			mergeKey:       "name",
			baseIndent:     "",
			mapEntryIndent: 2, // Parent key at column 0, so map entry at column 2
			want: []string{
				"  data:",
				"    emptyDir: {}",
			},
		},
		{
			name: "block-style sequence with two-space indentation",
			itemLines: []string{
				"  - name: config",
				"    configMap:",
				"      name: my-config",
			},
			mergeKey:       "name",
			baseIndent:     "  ",
			mapEntryIndent: 2, // Same as baseIndent
			want: []string{
				"  config:",
				"    configMap:",
				"      name: my-config",
			},
		},
		{
			name: "deeply nested with explicit indent",
			itemLines: []string{
				"    - name: nested",
				"      value: deep",
			},
			mergeKey:       "name",
			baseIndent:     "    ",
			mapEntryIndent: 4, // Same as baseIndent
			want: []string{
				"    nested:",
				"      value: deep",
			},
		},
		{
			name: "use default indent when mapEntryIndent is -1",
			itemLines: []string{
				"  - name: default",
				"    value: test",
			},
			mergeKey:       "name",
			baseIndent:     "  ",
			mapEntryIndent: -1, // Use baseIndent
			want: []string{
				"  default:",
				"    value: test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transformSingleItemWithIndent(tt.itemLines, tt.mergeKey, tt.baseIndent, tt.mapEntryIndent)

			if len(got) != len(tt.want) {
				t.Errorf("transformSingleItemWithIndent() returned %d lines, want %d\nGot:\n%s\nWant:\n%s",
					len(got), len(tt.want),
					strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
				return
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Line %d mismatch:\nGot:  %q\nWant: %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNeedsQuoting(t *testing.T) {
	t.Parallel()

	// Note: needsQuoting checks for YAML special characters that would break parsing
	// It doesn't quote all strings that COULD be quoted - only those that NEED it

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple string", "hello", false},
		{"string with spaces", "hello world", false}, // Spaces alone don't require quoting in YAML values
		{"string with colon", "key:value", true},
		{"string with quotes", `say "hello"`, true},
		{"numeric string", "123", false},
		{"path like string", "/etc/config", false},
		{"empty string", "", true},                             // Empty strings need quoting
		{"string starting with special char", "- item", false}, // This is a value, not a key, so - is ok
		{"string with newline", "line1\nline2", false},         // Actual newlines would be handled differently
		// Note: needsQuoting doesn't check YAML reserved words like true/false/null
		// These are used as map keys, and the plugin uses them as-is
		{"yaml boolean true", "true", false},
		{"yaml boolean false", "false", false},
		{"yaml null", "null", false},
		{"yaml yes", "yes", false},
		{"yaml no", "no", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needsQuoting(tt.input)
			if got != tt.want {
				t.Errorf("needsQuoting(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestDotPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path []string
		want string
	}{
		{"single segment", []string{"env"}, "env"},
		{"two segments", []string{"deployment", "env"}, "deployment.env"},
		{"multiple segments", []string{"app", "primary", "env"}, "app.primary.env"},
		{"empty path", []string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dotPath(tt.path)
			if got != tt.want {
				t.Errorf("dotPath(%v) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
