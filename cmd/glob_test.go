package main

import (
	"testing"
)

func TestMatchGlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		text    string
		want    bool
	}{
		// Exact matches
		{
			name:    "exact match single segment",
			pattern: "env",
			text:    "env",
			want:    true,
		},
		{
			name:    "exact match multiple segments",
			pattern: "deployment.env",
			text:    "deployment.env",
			want:    true,
		},
		{
			name:    "no match different text",
			pattern: "env",
			text:    "volumes",
			want:    false,
		},

		// Wildcard matches
		{
			name:    "single wildcard prefix",
			pattern: "*.env",
			text:    "deployment.env",
			want:    true,
		},
		{
			name:    "single wildcard prefix deep",
			pattern: "*.env",
			text:    "app.primary.env",
			want:    true,
		},
		{
			name:    "wildcard at end should not match",
			pattern: "env.*",
			text:    "env.value",
			want:    true,
		},
		{
			name:    "multiple wildcards",
			pattern: "*.*.env",
			text:    "app.primary.env",
			want:    true,
		},
		{
			name:    "wildcard matches any prefix",
			pattern: "*.volumeMounts",
			text:    "deployment.extraVolumeMounts",
			want:    false, // Not a suffix match
		},
		{
			name:    "suffix match without prefix segments",
			pattern: "*.env",
			text:    "env",
			want:    true, // Pattern consumed, remaining wildcards
		},

		// Edge cases
		{
			name:    "empty pattern",
			pattern: "",
			text:    "env",
			want:    false,
		},
		{
			name:    "empty text",
			pattern: "env",
			text:    "",
			want:    false,
		},
		{
			name:    "both empty",
			pattern: "",
			text:    "",
			want:    true,
		},
		{
			name:    "pattern longer than text without wildcards",
			pattern: "a.b.c.d",
			text:    "c.d",
			want:    false, // Non-wildcard segments must match
		},
		{
			name:    "pattern longer than text with wildcards",
			pattern: "*.*.c.d",
			text:    "c.d",
			want:    true, // Wildcards match missing segments
		},
		{
			name:    "text longer than pattern",
			pattern: "env",
			text:    "app.deployment.env",
			want:    true, // Pattern fully consumed
		},

		// Real-world patterns from built-in rules
		{
			name:    "volumeMounts pattern",
			pattern: "*.volumeMounts",
			text:    "volumeMounts",
			want:    true,
		},
		{
			name:    "nested volumeMounts",
			pattern: "*.volumeMounts",
			text:    "deployment.volumeMounts",
			want:    true,
		},
		{
			name:    "deeply nested volumeMounts",
			pattern: "*.volumeMounts",
			text:    "spec.template.spec.containers.volumeMounts",
			want:    true,
		},
		{
			name:    "extraVolumeMounts should not match volumeMounts",
			pattern: "*.volumeMounts",
			text:    "deployment.extraVolumeMounts",
			want:    false,
		},
		{
			name:    "extraVolumeMounts pattern",
			pattern: "*.extraVolumeMounts",
			text:    "deployment.extraVolumeMounts",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.text)
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.text, got, tt.want)
			}
		})
	}
}

func TestMatchRule(t *testing.T) {
	// matchRule matches against user-defined rules in conf.Rules
	// It does NOT match against built-in K8s types (those come from type introspection)

	// Save original config and restore after test
	originalConf := conf
	defer func() { conf = originalConf }()

	tests := []struct {
		name    string
		rules   []Rule
		path    []string
		wantNil bool
		wantKey string
	}{
		{
			name:    "no rules configured",
			rules:   nil,
			path:    []string{"env"},
			wantNil: true,
		},
		{
			name: "exact match rule",
			rules: []Rule{
				{PathPattern: "customField[]", UniqueKeys: []string{"id"}},
			},
			path:    []string{"customField"},
			wantNil: false,
			wantKey: "id",
		},
		{
			name: "wildcard match rule",
			rules: []Rule{
				{PathPattern: "*.metadata[]", UniqueKeys: []string{"name"}},
			},
			path:    []string{"dapr", "statestore", "metadata"},
			wantNil: false,
			wantKey: "name",
		},
		{
			name: "no match for different path",
			rules: []Rule{
				{PathPattern: "customField[]", UniqueKeys: []string{"id"}},
			},
			path:    []string{"otherField"},
			wantNil: true,
		},
		{
			name: "first matching rule wins",
			rules: []Rule{
				{PathPattern: "*.env[]", UniqueKeys: []string{"name"}},
				{PathPattern: "deployment.env[]", UniqueKeys: []string{"key"}},
			},
			path:    []string{"deployment", "env"},
			wantNil: false,
			wantKey: "name",
		},
		{
			name: "deeply nested path with wildcard",
			rules: []Rule{
				{PathPattern: "*.extraVolumeMounts[]", UniqueKeys: []string{"mountPath"}},
			},
			path:    []string{"deployment", "extraVolumeMounts"},
			wantNil: false,
			wantKey: "mountPath",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up test rules
			conf.Rules = tt.rules

			rule := matchRule(tt.path)
			if tt.wantNil {
				if rule != nil {
					t.Errorf("matchRule(%v) = %+v, want nil", tt.path, rule)
				}
				return
			}
			if rule == nil {
				t.Fatalf("matchRule(%v) = nil, want rule with key %q", tt.path, tt.wantKey)
			}
			// Check that one of the unique keys matches
			found := false
			for _, k := range rule.UniqueKeys {
				if k == tt.wantKey {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("matchRule(%v).UniqueKeys = %v, want to contain %q", tt.path, rule.UniqueKeys, tt.wantKey)
			}
		})
	}
}
