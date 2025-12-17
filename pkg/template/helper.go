package template

import (
	"path/filepath"
	"strings"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/fs"
)

// EnsureHelpersWithReport creates helper template and returns true if created
func EnsureHelpersWithReport(filesystem fs.FileSystem, root string) bool {
	path := filepath.Join(root, "templates", "_listmap.tpl")
	if _, err := filesystem.Stat(path); err == nil {
		return false // Already exists
	}
	err := filesystem.WriteFile(path, []byte(strings.TrimSpace(ListMapHelper())+"\n"), 0644)
	return err == nil
}

// ListMapHelper returns a helper template that renders map items as a YAML list
// Parameters:
//   - items: the map of items (keyed by merge key value)
//   - key: the patchMergeKey field name (e.g., "name", "mountPath", "containerPort")
//
// Output: YAML list items without section name, suitable for use with nindent
//
// Note: This helper uses Helm-specific functions: keys, sortAlpha, get, quote, toYaml, indent
func ListMapHelper() string {
	return `
{{- define "chart.listmap.items" -}}
{{- $items := .items -}}
{{- $key := .key -}}
{{- range $keyVal := keys $items | sortAlpha }}
{{- $spec := get $items $keyVal }}
- {{ $key }}: {{ $keyVal | quote }}
{{- if $spec }}
{{ toYaml $spec | indent 2 }}
{{- end }}
{{- end }}
{{- end -}}`
}
