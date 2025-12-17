package parser

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

// TemplateDirective represents a Go template directive found in a K8s manifest
type TemplateDirective struct {
	YAMLPath    string // e.g., spec.template.spec.volumes
	Content     string // The template content (e.g., "{{- toYaml .Values.volumes | nindent 8 }}")
	LineNumber  int
	FilePath    string
	WithContext string // If inside a "with .Values.X" block, the Values path
}

// ParsedTemplate represents a parsed Helm template file
type ParsedTemplate struct {
	FilePath   string
	APIVersion string
	Kind       string
	GoType     reflect.Type
	Directives []TemplateDirective
}

// ConversionCandidate represents a field that can be converted to map format
type ConversionCandidate struct {
	ValuesPath    string       // Path in values.yaml (e.g., "volumes")
	YAMLPath      string       // Path in K8s resource (e.g., "spec.template.spec.volumes")
	GoType        reflect.Type // Element type (e.g., corev1.Volume)
	UniqueKeyName string       // Required unique field (e.g., "name")
	TemplatePath  string       // Source template file
	UsesInclude   bool         // Whether it uses include (vs direct .Values)
	IncludeChain  []string     // Chain of includes followed, if any
}

// parseTemplateFile parses a Helm template and extracts K8s resource info and directives
func ParseTemplateFile(templatePath string) (*ParsedTemplate, error) {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, err
	}

	result := &ParsedTemplate{
		FilePath: templatePath,
	}

	lines := strings.Split(string(content), "\n")

	// Extract apiVersion and kind
	result.APIVersion, result.Kind = extractAPIVersionAndKind(lines)
	if result.APIVersion == "" || result.Kind == "" {
		// Skip templates without explicit apiVersion/kind
		return result, nil
	}

	// Resolve Go type (may be nil for CRDs)
	// Note: GoType is not resolved here to avoid import cycle with pkg/k8s
	// Caller should resolve using k8s.ResolveKubeAPIType if needed

	// Extract template directives with their YAML paths
	// This is needed for both built-in K8s types and CRDs
	result.Directives = extractDirectives(lines, templatePath)

	return result, nil
}

// extractAPIVersionAndKind extracts apiVersion and kind from template lines
// Only handles explicit values (not templated)
func extractAPIVersionAndKind(lines []string) (apiVersion, kind string) {
	reAPIVersion := regexp.MustCompile(`^apiVersion:\s*(.+)`)
	reKind := regexp.MustCompile(`^kind:\s*(.+)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if m := reAPIVersion.FindStringSubmatch(line); m != nil {
			val := strings.TrimSpace(m[1])
			// Strip quotes if present (some templates use apiVersion: "networking.k8s.io/v1")
			val = strings.Trim(val, `"'`)
			// Skip if templated
			if !strings.Contains(val, "{{") {
				apiVersion = val
			}
		}

		if m := reKind.FindStringSubmatch(line); m != nil {
			val := strings.TrimSpace(m[1])
			// Strip quotes if present
			val = strings.Trim(val, `"'`)
			// Skip if templated
			if !strings.Contains(val, "{{") {
				kind = val
			}
		}

		if apiVersion != "" && kind != "" {
			break
		}
	}

	return
}

// withBlockContext tracks a "with .Values.X" block
type withBlockContext struct {
	valuesPath string // The .Values path (e.g., "prometheusOperator.extraVolumeMounts")
	indent     int    // Indentation level where the with started
}

// extractDirectives finds template directives and their YAML path context
func extractDirectives(lines []string, filePath string) []TemplateDirective {
	var directives []TemplateDirective

	// Track YAML path via indentation
	var pathStack []pathLevel

	// Track with block contexts for resolving "toYaml ." patterns
	var withStack []withBlockContext

	// Regex patterns
	reYAMLKey := regexp.MustCompile(`^(\s*)([a-zA-Z_][a-zA-Z0-9_-]*):\s*(.*)`)
	reTemplateDirective := regexp.MustCompile(`\{\{.*\}\}`)
	reListItem := regexp.MustCompile(`^(\s*)-\s*`)

	// Pattern to detect "with .Values.X"
	reWithValues := regexp.MustCompile(`\{\{-?\s*with\s+\.Values\.([a-zA-Z0-9_.]+)\s*-?\}\}`)
	// Pattern to detect "end" closing a block
	reEnd := regexp.MustCompile(`\{\{-?\s*end\s*-?\}\}`)

	for lineNum, line := range lines {
		// Skip empty lines and comments
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		// Check for "with .Values.X" opening a new context
		if m := reWithValues.FindStringSubmatch(line); m != nil {
			withStack = append(withStack, withBlockContext{
				valuesPath: m[1],
				indent:     indent,
			})
		}

		// Check for "end" that might close a with block
		if reEnd.MatchString(line) && len(withStack) > 0 {
			// Pop the most recent with block at or after this indent level
			// Note: This is a simplification - real Go template parsing would need proper nesting
			withStack = withStack[:len(withStack)-1]
		}

		// Check for YAML key
		if m := reYAMLKey.FindStringSubmatch(line); m != nil {
			keyIndent := len(m[1])
			key := m[2]
			value := m[3]

			// Pop stack until we find parent level
			for len(pathStack) > 0 && pathStack[len(pathStack)-1].indent >= keyIndent {
				pathStack = pathStack[:len(pathStack)-1]
			}

			// Push current key
			pathStack = append(pathStack, pathLevel{indent: keyIndent, key: key})

			// Check if value contains a template directive
			if reTemplateDirective.MatchString(value) {
				yamlPath := buildYAMLPath(pathStack)
				// Capture current with context
				var withContext string
				if len(withStack) > 0 {
					withContext = withStack[len(withStack)-1].valuesPath
				}
				directives = append(directives, TemplateDirective{
					YAMLPath:    yamlPath,
					Content:     strings.TrimSpace(value),
					LineNumber:  lineNum + 1,
					FilePath:    filePath,
					WithContext: withContext,
				})
			}
			continue
		}

		// Check for list item with template
		if m := reListItem.FindStringSubmatch(line); m != nil {
			listIndent := len(m[1])
			// Adjust stack for list context
			for len(pathStack) > 0 && pathStack[len(pathStack)-1].indent >= listIndent {
				pathStack = pathStack[:len(pathStack)-1]
			}
		}

		// Check for standalone template directive line
		if reTemplateDirective.MatchString(line) && !reYAMLKey.MatchString(line) {
			// Build YAML path excluding items deeper than this directive's indentation.
			// We don't permanently modify pathStack because template directives don't
			// establish YAML structure - they just need to know their context.
			// However, items at indent > this line's indent are not part of our context.
			var contextStack []pathLevel
			for _, level := range pathStack {
				if level.indent <= indent {
					contextStack = append(contextStack, level)
				}
			}
			yamlPath := buildYAMLPath(contextStack)
			// Capture current with context
			var withContext string
			if len(withStack) > 0 {
				withContext = withStack[len(withStack)-1].valuesPath
			}
			directives = append(directives, TemplateDirective{
				YAMLPath:    yamlPath,
				Content:     trimmed,
				LineNumber:  lineNum + 1,
				FilePath:    filePath,
				WithContext: withContext,
			})
		}
	}

	return directives
}

// pathLevel tracks indentation and key name for YAML path building
type pathLevel struct {
	indent int
	key    string
}

// buildYAMLPath constructs a dot-separated path from the stack
func buildYAMLPath(stack []pathLevel) string {
	var parts []string
	for _, level := range stack {
		parts = append(parts, level.key)
	}
	return strings.Join(parts, ".")
}

// ValuesUsage represents how .Values is used in a template
type ValuesUsage struct {
	ValuesPath string // e.g., "volumes" or "image.tag"
	Pattern    string // "toYaml", "range", "range_kv", "with", "direct"
	IsListUse  bool   // true if used as a list (toYaml, range without k/v)
}

// analyzeDirectiveContent extracts .Values usage from a template directive
// withContext is provided when the directive is inside a "with .Values.X" block
func AnalyzeDirectiveContent(content string, withContext string) []ValuesUsage {
	var usages []ValuesUsage

	// Pattern: toYaml .Values.X
	reToYaml := regexp.MustCompile(`toYaml\s+\.Values\.([a-zA-Z0-9_.]+)`)
	for _, m := range reToYaml.FindAllStringSubmatch(content, -1) {
		usages = append(usages, ValuesUsage{
			ValuesPath: m[1],
			Pattern:    "toYaml",
			IsListUse:  true,
		})
	}

	// Pattern: toYaml . (dot context - uses the enclosing "with" block's path)
	// Only match if there's a withContext and the content uses just "."
	if withContext != "" {
		reToYamlDot := regexp.MustCompile(`toYaml\s+\.(\s*[|}\s])`)
		if reToYamlDot.MatchString(content) {
			usages = append(usages, ValuesUsage{
				ValuesPath: withContext,
				Pattern:    "toYaml_dot",
				IsListUse:  true,
			})
		}
	}

	// Pattern: with .Values.X
	reWith := regexp.MustCompile(`with\s+\.Values\.([a-zA-Z0-9_.]+)`)
	for _, m := range reWith.FindAllStringSubmatch(content, -1) {
		usages = append(usages, ValuesUsage{
			ValuesPath: m[1],
			Pattern:    "with",
			IsListUse:  true, // Often followed by toYaml inside
		})
	}

	// Pattern: range $k, $v := .Values.X (map pattern - already converted)
	reRangeKV := regexp.MustCompile(`range\s+\$\w+\s*,\s*\$\w+\s*:=\s*\.Values\.([a-zA-Z0-9_.]+)`)
	for _, m := range reRangeKV.FindAllStringSubmatch(content, -1) {
		usages = append(usages, ValuesUsage{
			ValuesPath: m[1],
			Pattern:    "range_kv",
			IsListUse:  false, // Map pattern
		})
	}

	// Pattern: range .Values.X (single var - list pattern)
	reRange := regexp.MustCompile(`range\s+\.Values\.([a-zA-Z0-9_.]+)`)
	for _, m := range reRange.FindAllStringSubmatch(content, -1) {
		// Skip if already matched as range_kv
		found := false
		for _, u := range usages {
			if u.ValuesPath == m[1] && u.Pattern == "range_kv" {
				found = true
				break
			}
		}
		if !found {
			usages = append(usages, ValuesUsage{
				ValuesPath: m[1],
				Pattern:    "range",
				IsListUse:  true,
			})
		}
	}

	return usages
}

// hasIncludeDirective checks if content contains an include directive
func HasIncludeDirective(content string) bool {
	return strings.Contains(content, "include ")
}

// loadTemplateContent loads the content of a named template from _helpers.tpl or similar
func loadTemplateContent(templatesDir, templateName string) (string, error) {
	// Search in all .tpl files
	var content string

	err := filepath.WalkDir(templatesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".tpl") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Look for {{- define "templateName" -}}
		definePattern := fmt.Sprintf(`\{\{-?\s*define\s+"%s"\s*-?\}\}`, regexp.QuoteMeta(templateName))
		re := regexp.MustCompile(definePattern)
		if re.MatchString(string(data)) {
			// Extract the content between define and end
			content = extractDefinedTemplate(string(data), templateName)
			return filepath.SkipAll
		}
		return nil
	})

	if content == "" && err == nil {
		return "", fmt.Errorf("template %q not found", templateName)
	}

	return content, err
}

// extractDefinedTemplate extracts the content of a defined template
func extractDefinedTemplate(fileContent, templateName string) string {
	definePattern := fmt.Sprintf(`\{\{-?\s*define\s+"%s"\s*-?\}\}`, regexp.QuoteMeta(templateName))
	reDefine := regexp.MustCompile(definePattern)
	reEnd := regexp.MustCompile(`\{\{-?\s*end\s*-?\}\}`)

	loc := reDefine.FindStringIndex(fileContent)
	if loc == nil {
		return ""
	}

	// Find matching end (handle nested defines by counting)
	rest := fileContent[loc[1]:]
	depth := 1
	scanner := bufio.NewScanner(strings.NewReader(rest))
	var lines []string

	for scanner.Scan() {
		line := scanner.Text()

		// Count define/end balance
		if strings.Contains(line, "define ") {
			depth++
		}
		if reEnd.MatchString(line) {
			depth--
			if depth == 0 {
				break
			}
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// followIncludeChain recursively follows include directives to find .Values usage
// withContext is passed through when the include is inside a "with .Values.X" block
func FollowIncludeChain(templatesDir, content, withContext string, visited map[string]bool) []ValuesUsage {
	var allUsages []ValuesUsage

	// First check for direct .Values usage
	usages := AnalyzeDirectiveContent(content, withContext)
	allUsages = append(allUsages, usages...)

	// Check for includes and follow them
	re := regexp.MustCompile(`include\s+"([^"]+)"`)
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		templateName := m[1]

		// Prevent infinite loops
		if visited[templateName] {
			continue
		}
		visited[templateName] = true

		// Load and analyze the included template
		includedContent, err := loadTemplateContent(templatesDir, templateName)
		if err != nil {
			continue
		}

		// Recursively follow (pass through withContext)
		nestedUsages := FollowIncludeChain(templatesDir, includedContent, withContext, visited)
		allUsages = append(allUsages, nestedUsages...)
	}

	return allUsages
}
