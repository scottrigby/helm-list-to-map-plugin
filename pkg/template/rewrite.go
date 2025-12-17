package template

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// RewriteTemplatesWithBackups rewrites templates and tracks backup files
func RewriteTemplatesWithBackups(chartPath string, paths []PathInfo, backupExtension string, existingBackups []string) ([]string, []string, error) {
	var changed []string
	backups := existingBackups
	tdir := filepath.Join(chartPath, "templates")
	err := filepath.WalkDir(tdir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".tpl") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		orig := string(data)
		newContent := orig

		for _, p := range paths {
			// Use single generic helper for all conversions
			newContent, _ = ReplaceListBlocks(newContent, p.DotPath, p.MergeKey, p.SectionName)
		}

		if newContent != orig {
			backupPath := path + backupExtension
			if err := backupFile(path, backupExtension, data); err != nil {
				return err
			}
			backups = append(backups, backupPath)
			if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
				return err
			}
			changed = append(changed, rel(chartPath, path))
		}
		return nil
	})
	return changed, backups, err
}

// ReplaceListBlocks replaces toYaml calls for list fields with the listmap.items helper
// Parameters:
//   - dotPath: the .Values path (e.g., "volumes", "deployment.env")
//   - mergeKey: the patchMergeKey from K8s API (e.g., "name", "mountPath")
//   - sectionName: unused, kept for compatibility
//
// Returns: (updated template content, whether any replacements were made)
func ReplaceListBlocks(tpl, dotPath, mergeKey, _ string) (string, bool) {
	origLen := len(tpl)
	escapedDotPath := regexp.QuoteMeta(dotPath)

	// Helper call generator - just replaces toYaml with our helper, preserving the nindent
	helperCall := func(indent int) string {
		return fmt.Sprintf(`{{- include "chart.listmap.items" (dict "items" (index .Values %s) "key" %q) | nindent %d }}`,
			QuotePath(dotPath), mergeKey, indent)
	}

	// Pattern 1: {{- toYaml .Values.X | nindent N }}
	// Direct toYaml with nindent - most common pattern
	re1 := regexp.MustCompile(`\{\{-?\s*toYaml\s+\.Values\.` + escapedDotPath + `\s*\|\s*nindent\s*(\d+)\s*\}\}`)
	tpl = re1.ReplaceAllStringFunc(tpl, func(match string) string {
		submatches := re1.FindStringSubmatch(match)
		if len(submatches) > 1 {
			indent, _ := strconv.Atoi(submatches[1])
			return helperCall(indent)
		}
		return match
	})

	// Pattern 2: {{ toYaml .Values.X | indent N }}
	// toYaml with indent (no leading dash, no 'n' prefix)
	re2 := regexp.MustCompile(`\{\{\s*toYaml\s+\.Values\.` + escapedDotPath + `\s*\|\s*indent\s*(\d+)\s*\}\}`)
	tpl = re2.ReplaceAllStringFunc(tpl, func(match string) string {
		submatches := re2.FindStringSubmatch(match)
		if len(submatches) > 1 {
			indent, _ := strconv.Atoi(submatches[1])
			// indent doesn't add newline, but our helper expects nindent
			// Use nindent with same value (adds leading newline which is usually desired)
			return helperCall(indent)
		}
		return match
	})

	// Pattern 3: {{- with .Values.X }}...{{- toYaml . | nindent N }}...{{- end }}
	// "with" block pattern - replace the whole block, preserving leading whitespace
	re3 := regexp.MustCompile(`(?ms)([ \t]*)\{\{-?\s*with\s+\.Values\.` + escapedDotPath + `\s*\}\}\s*(\S+):\s*\n\s*\{\{-?\s*toYaml\s+\.\s*\|\s*nindent\s*(\d+)\s*\}\}\s*\{\{-?\s*end\s*\}\}`)
	tpl = re3.ReplaceAllStringFunc(tpl, func(match string) string {
		submatches := re3.FindStringSubmatch(match)
		if len(submatches) > 3 {
			leadingSpace := submatches[1]
			sectionName := submatches[2]
			indent, _ := strconv.Atoi(submatches[3])
			// Keep the section name with proper indentation
			return fmt.Sprintf(`%s{{- if (index .Values %s) }}
%s%s:
%s
%s{{- end }}`, leadingSpace, QuotePath(dotPath), leadingSpace, sectionName, helperCall(indent), leadingSpace)
		}
		return match
	})

	// Pattern 4: {{- if .Values.X }}\n  section:\n...toYaml...{{- end }}
	// Conditional block with section - replace toYaml inside
	re4 := regexp.MustCompile(`(?ms)(\{\{-?\s*if\s+\.Values\.` + escapedDotPath + `\s*\}\}\s*\n\s*\S+:\s*\n\s*)\{\{-?\s*toYaml\s+\.Values\.` + escapedDotPath + `\s*\|\s*nindent\s*(\d+)\s*\}\}(\s*\n\{\{-?\s*end\s*\}\})`)
	tpl = re4.ReplaceAllStringFunc(tpl, func(match string) string {
		submatches := re4.FindStringSubmatch(match)
		if len(submatches) > 3 {
			prefix := submatches[1]
			indent, _ := strconv.Atoi(submatches[2])
			suffix := submatches[3]
			return prefix + helperCall(indent) + suffix
		}
		return match
	})

	// Pattern 5: {{- range .Values.X }}...{{- end }}
	// Range loop pattern - capture the indent from context
	re5 := regexp.MustCompile(`(?ms)([ \t]*)(\S+):\s*\n\s*\{\{-?\s*range\s+\.Values\.` + escapedDotPath + `\s*\}\}.*?\{\{-?\s*end\s*\}\}`)
	tpl = re5.ReplaceAllStringFunc(tpl, func(match string) string {
		submatches := re5.FindStringSubmatch(match)
		if len(submatches) > 2 {
			leadingSpace := submatches[1]
			sectionName := submatches[2]
			indent := len(leadingSpace) + 2 // section indent + 2 for list items
			return fmt.Sprintf(`{{- if (index .Values %s) }}
%s%s:
%s
{{- end }}`, QuotePath(dotPath), leadingSpace, sectionName, helperCall(indent))
		}
		return match
	})

	// Pattern 6: Existing old-style helper calls - update to new format
	re6 := regexp.MustCompile(`\{\{-?\s*include\s+"chart\.\S+\.render"\s*\(dict\s+"\S+"\s*\(index\s+\.Values\s+` + regexp.QuoteMeta(QuotePath(dotPath)) + `\)\)\s*\}\}`)
	if re6.MatchString(tpl) {
		// Just mark as changed - these need manual review since we don't know the indent
		tpl = re6.ReplaceAllString(tpl, helperCall(8)) // Default indent
	}

	changed := len(tpl) != origLen
	return tpl, changed
}

// CheckTemplatePatterns checks which paths have matching template patterns without modifying files
// Returns a map of dotPath -> true if the path has a matching template pattern
func CheckTemplatePatterns(chartPath string, paths []PathInfo) map[string]bool {
	matched := make(map[string]bool)
	tdir := filepath.Join(chartPath, "templates")
	_ = filepath.WalkDir(tdir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".tpl") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		for _, p := range paths {
			if matched[p.DotPath] {
				continue // Already found a match
			}
			_, changed := ReplaceListBlocks(content, p.DotPath, p.MergeKey, p.SectionName)
			if changed {
				matched[p.DotPath] = true
			}
		}
		return nil
	})
	return matched
}

// QuotePath converts a dotted path to quoted index format
// e.g., "a.b.c" -> `"a" "b" "c"`
func QuotePath(dotPath string) string {
	parts := strings.Split(dotPath, ".")
	var quoted []string
	for _, p := range parts {
		quoted = append(quoted, fmt.Sprintf("%q", p))
	}
	return strings.Join(quoted, " ")
}

func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return p
}

func backupFile(path, ext string, original []byte) error {
	return os.WriteFile(path+ext, original, 0644)
}
