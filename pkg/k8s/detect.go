package k8s

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/crd"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/detect"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/parser"
	"gopkg.in/yaml.v3"
)

// DetectedCandidate is exported from pkg/detect
type DetectedCandidate = detect.DetectedCandidate

// UndetectedCategory represents why a field couldn't be auto-detected
type UndetectedCategory string

const (
	// CategoryCRDNoKeys - CRD loaded, field is confirmed array, but lacks x-kubernetes-list-map-keys
	CategoryCRDNoKeys UndetectedCategory = "crd_no_keys"
	// CategoryK8sNoKeys - K8s type, field is confirmed slice, but lacks patchMergeKey
	CategoryK8sNoKeys UndetectedCategory = "k8s_no_keys"
	// CategoryMissingCRD - Custom Resource but CRD not loaded
	CategoryMissingCRD UndetectedCategory = "missing_crd"
	// CategoryUnknownType - No type information available (can't determine if array)
	CategoryUnknownType UndetectedCategory = "unknown_type"
)

// UndetectedUsage represents a .Values list usage that couldn't be auto-detected
type UndetectedUsage struct {
	ValuesPath   string             // Path in values.yaml
	TemplateFile string             // Template file where this was found
	LineNumber   int                // Line number in template
	Reason       string             // Why it couldn't be detected
	Suggestion   string             // What the user can do about it
	APIVersion   string             // API version of the resource (if known)
	Kind         string             // Kind of the resource (if known)
	Category     UndetectedCategory // Why detection failed
}

// PartialTemplate represents a template without apiVersion/kind (helper/partial)
type PartialTemplate struct {
	FilePath     string   // Relative path to the template file
	DefinedNames []string // Template names defined via {{- define "..." }}
	ValuesUsages []string // .Values paths used in this partial
	IncludedFrom []string // Resource templates that include this partial
}

// DetectionResult combines all detection outputs
type DetectionResult struct {
	Candidates []DetectedCandidate
	Undetected []UndetectedUsage
	Partials   []PartialTemplate
}

// detectConversionCandidates scans templates for convertible fields using K8s API introspection
// and CRD registry lookup
func DetectConversionCandidates(chartRoot string) ([]DetectedCandidate, error) {
	var candidates []DetectedCandidate
	seen := make(map[string]bool) // dedup by valuesPath

	templatesDir := filepath.Join(chartRoot, "templates")

	err := filepath.WalkDir(templatesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		// Parse template file
		parsed, err := parser.ParseTemplateFile(path)
		if err != nil {
			return nil // Skip problematic files
		}

		// Resolve Go type from apiVersion/kind (parser doesn't do this to avoid import cycle)
		if parsed.APIVersion != "" && parsed.Kind != "" && parsed.GoType == nil {
			parsed.GoType = ResolveKubeAPIType(parsed.APIVersion, parsed.Kind)
		}

		// Check if we can resolve this type (either built-in K8s or CRD)
		hasCRDType := parsed.APIVersion != "" && parsed.Kind != "" &&
			crd.GetGlobalRegistry().HasType(parsed.APIVersion, parsed.Kind)

		// Skip if no K8s type resolved and no CRD type available
		if parsed.GoType == nil && !hasCRDType {
			return nil
		}

		// Process each directive
		for _, directive := range parsed.Directives {
			// Extract what .Values paths are being used
			var valuesUsages []parser.ValuesUsage
			if parser.HasIncludeDirective(directive.Content) {
				visited := make(map[string]bool)
				valuesUsages = parser.FollowIncludeChain(templatesDir, directive.Content, directive.WithContext, visited)
			} else {
				valuesUsages = parser.AnalyzeDirectiveContent(directive.Content, directive.WithContext)
			}

			for _, usage := range valuesUsages {
				if !usage.IsListUse {
					continue // Already using map pattern
				}

				// Skip "with" pattern itself - we only care about actual rendering (toYaml)
				// The "with" just opens a scope; the "toYaml_dot" inside is what renders data
				if usage.Pattern == "with" {
					continue
				}

				if seen[usage.ValuesPath] {
					continue
				}

				// The directive's YAMLPath tells us exactly where in the K8s structure
				// this value is rendered (e.g., "spec.template.spec.securityContext").
				// The values key name (e.g., "podSecurityContext") is irrelevant for
				// schema lookup - we use the actual K8s YAML path.
				fullYAMLPath := directive.YAMLPath
				if fullYAMLPath == "" {
					// Rare case: directive at root level with no parent keys
					continue
				}
				sectionName := GetLastPathSegment(usage.ValuesPath)

				// Check if this path points to a convertible field
				// Try built-in K8s types first, then CRD registry
				var fieldInfo *FieldInfo
				if parsed.GoType != nil {
					fieldInfo = IsConvertibleField(parsed.GoType, fullYAMLPath)
				}
				if fieldInfo == nil && hasCRDType {
					fieldInfo = convertCRDFieldInfo(crd.IsConvertibleCRDField(parsed.APIVersion, parsed.Kind, fullYAMLPath))
				}
				if fieldInfo == nil {
					continue
				}

				seen[usage.ValuesPath] = true

				// Build element type name
				var elemTypeName string
				if fieldInfo.ElementType != nil {
					elemTypeName = FormatTypeName(fieldInfo.ElementType)
				} else {
					elemTypeName = "map[string]interface{}" // CRD types don't have Go types
				}

				// Get relative template filename
				templateFile := filepath.Base(path)

				candidates = append(candidates, DetectedCandidate{
					ValuesPath:   usage.ValuesPath,
					YAMLPath:     fullYAMLPath,
					MergeKey:     fieldInfo.MergeKey,
					ElementType:  elemTypeName,
					SectionName:  sectionName,
					ResourceKind: parsed.Kind,
					TemplateFile: templateFile,
				})
			}
		}

		return nil
	})

	return candidates, err
}

// GetLastPathSegment returns the last segment of a dot-separated path
func GetLastPathSegment(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

// detectConversionCandidatesFull scans templates and returns full detection results including
// undetected usages and partial templates
func DetectConversionCandidatesFull(chartRoot string) (*DetectionResult, error) {
	result := &DetectionResult{}
	seen := make(map[string]bool)           // dedup candidates by valuesPath
	seenUndetected := make(map[string]bool) // dedup undetected by valuesPath

	templatesDir := filepath.Join(chartRoot, "templates")

	// First pass: scan for partial templates
	partials, includeMap := ScanPartialTemplates(templatesDir)
	result.Partials = partials

	// Second pass: scan resource templates
	err := filepath.WalkDir(templatesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		// Parse template file
		parsed, err := parser.ParseTemplateFile(path)
		if err != nil {
			return nil // Skip problematic files
		}

		// Resolve Go type from apiVersion/kind (parser doesn't do this to avoid import cycle)
		if parsed.APIVersion != "" && parsed.Kind != "" && parsed.GoType == nil {
			parsed.GoType = ResolveKubeAPIType(parsed.APIVersion, parsed.Kind)
		}

		templateFile := filepath.Base(path)

		// Check if we can resolve this type (either built-in K8s or CRD)
		hasCRDType := parsed.APIVersion != "" && parsed.Kind != "" &&
			crd.GetGlobalRegistry().HasType(parsed.APIVersion, parsed.Kind)

		// Track which partials are included from this resource template
		for _, directive := range parsed.Directives {
			if parser.HasIncludeDirective(directive.Content) {
				includedNames := extractIncludeNames(directive.Content)
				for _, name := range includedNames {
					includeMap[name] = append(includeMap[name], templateFile)
				}
			}
		}

		// If no K8s type resolved and no CRD type available, track undetected usages
		if parsed.GoType == nil && !hasCRDType {
			// Still scan for .Values list usages to report as undetected
			for _, directive := range parsed.Directives {
				var valuesUsages []parser.ValuesUsage
				if parser.HasIncludeDirective(directive.Content) {
					visited := make(map[string]bool)
					valuesUsages = parser.FollowIncludeChain(templatesDir, directive.Content, directive.WithContext, visited)
				} else {
					valuesUsages = parser.AnalyzeDirectiveContent(directive.Content, directive.WithContext)
				}

				for _, usage := range valuesUsages {
					if !usage.IsListUse || usage.Pattern == "with" {
						continue
					}
					if seenUndetected[usage.ValuesPath] {
						continue
					}
					seenUndetected[usage.ValuesPath] = true

					var reason, suggestion string
					var category UndetectedCategory
					if parsed.APIVersion != "" && parsed.Kind != "" {
						reason = fmt.Sprintf("Custom Resource %s/%s without loaded CRD", parsed.APIVersion, parsed.Kind)
						suggestion = fmt.Sprintf("helm list-to-map add-rule --path='%s[]' --uniqueKey=name", usage.ValuesPath)
						category = CategoryMissingCRD
					} else {
						reason = "Unknown resource type"
						suggestion = fmt.Sprintf("helm list-to-map add-rule --path='%s[]' --uniqueKey=name", usage.ValuesPath)
						category = CategoryUnknownType
					}

					result.Undetected = append(result.Undetected, UndetectedUsage{
						ValuesPath:   usage.ValuesPath,
						TemplateFile: templateFile,
						LineNumber:   directive.LineNumber,
						Reason:       reason,
						Suggestion:   suggestion,
						APIVersion:   parsed.APIVersion,
						Kind:         parsed.Kind,
						Category:     category,
					})
				}
			}
			return nil
		}

		// Process each directive for convertible fields
		for _, directive := range parsed.Directives {
			// Extract what .Values paths are being used
			var valuesUsages []parser.ValuesUsage
			if parser.HasIncludeDirective(directive.Content) {
				visited := make(map[string]bool)
				valuesUsages = parser.FollowIncludeChain(templatesDir, directive.Content, directive.WithContext, visited)
			} else {
				valuesUsages = parser.AnalyzeDirectiveContent(directive.Content, directive.WithContext)
			}

			for _, usage := range valuesUsages {
				if !usage.IsListUse {
					continue // Already using map pattern
				}

				// Skip "with" pattern itself - we only care about actual rendering (toYaml)
				// The "with" just opens a scope; the "toYaml_dot" inside is what renders data
				if usage.Pattern == "with" {
					continue
				}

				if seen[usage.ValuesPath] {
					continue
				}

				// The directive's YAMLPath tells us exactly where in the K8s structure
				// this value is rendered (e.g., "spec.template.spec.securityContext").
				// The values key name (e.g., "podSecurityContext") is irrelevant for
				// schema lookup - we use the actual K8s YAML path.
				fullYAMLPath := directive.YAMLPath
				if fullYAMLPath == "" {
					// Rare case: directive at root level with no parent keys
					continue
				}
				sectionName := GetLastPathSegment(usage.ValuesPath)

				// Check if this path points to a convertible field
				// Try built-in K8s types first, then CRD registry
				var fieldInfo *FieldInfo
				fieldCheck := FieldNotFound
				if parsed.GoType != nil {
					fieldCheck, fieldInfo = CheckFieldType(parsed.GoType, fullYAMLPath)
				}
				// If it's not a slice in the K8s type, skip it entirely - it's not a list field
				// (e.g., resources, affinity, nodeSelector are structs/maps, not lists)
				if fieldCheck == FieldNotSlice {
					continue
				}
				// For slices without merge key, try CRD registry as fallback
				// Also try CRD if K8s type exists but has no patchMergeKey
				if (fieldInfo == nil || fieldInfo.MergeKey == "") && hasCRDType {
					crdInfo := crd.IsConvertibleCRDField(parsed.APIVersion, parsed.Kind, fullYAMLPath)
					if crdInfo != nil {
						fieldInfo = convertCRDFieldInfo(crdInfo)
					}
				}

				// No merge key found from K8s types or CRD registry
				if fieldInfo == nil || fieldInfo.MergeKey == "" {
					// Field is either:
					// 1. A slice without patchMergeKey (fieldCheck == FieldSliceNoKey)
					// 2. Not found in K8s types but might be in CRD (fieldCheck == FieldNotFound)

					// For CRD types, only report as "potentially convertible" if the field is actually
					// an array in the CRD schema. toYaml is used for maps, objects, AND arrays - we
					// shouldn't suggest conversion for non-array fields.
					if hasCRDType && fieldCheck != FieldSliceNoKey {
						isArray := crd.IsCRDArrayField(parsed.APIVersion, parsed.Kind, fullYAMLPath)
						if !isArray {
							// Not an array in CRD schema - skip (it's a map/object being rendered)
							continue
						}
					}

					if !seenUndetected[usage.ValuesPath] {
						seenUndetected[usage.ValuesPath] = true
						var reason, suggestion string
						var category UndetectedCategory
						if fieldCheck == FieldSliceNoKey {
							reason = fmt.Sprintf("Slice field %s has no patchMergeKey", fullYAMLPath)
							suggestion = fmt.Sprintf("helm list-to-map add-rule --path='%s[]' --uniqueKey=name", usage.ValuesPath)
							category = CategoryK8sNoKeys
						} else if hasCRDType {
							reason = fmt.Sprintf("Array field %s lacks x-kubernetes-list-map-keys", fullYAMLPath)
							suggestion = fmt.Sprintf("helm list-to-map add-rule --path='%s[]' --uniqueKey=name", usage.ValuesPath)
							category = CategoryCRDNoKeys
						} else {
							reason = "Field not found in K8s type schema"
							suggestion = fmt.Sprintf("helm list-to-map add-rule --path='%s[]' --uniqueKey=name", usage.ValuesPath)
							category = CategoryUnknownType
						}
						result.Undetected = append(result.Undetected, UndetectedUsage{
							ValuesPath:   usage.ValuesPath,
							TemplateFile: templateFile,
							LineNumber:   directive.LineNumber,
							Reason:       reason,
							Suggestion:   suggestion,
							APIVersion:   parsed.APIVersion,
							Kind:         parsed.Kind,
							Category:     category,
						})
					}
					continue
				}

				seen[usage.ValuesPath] = true

				// Build element type name
				var elemTypeName string
				if fieldInfo.ElementType != nil {
					elemTypeName = FormatTypeName(fieldInfo.ElementType)
				} else {
					elemTypeName = "map[string]interface{}" // CRD types don't have Go types
				}

				result.Candidates = append(result.Candidates, DetectedCandidate{
					ValuesPath:   usage.ValuesPath,
					YAMLPath:     fullYAMLPath,
					MergeKey:     fieldInfo.MergeKey,
					ElementType:  elemTypeName,
					SectionName:  sectionName,
					ResourceKind: parsed.Kind,
					TemplateFile: templateFile,
				})
			}
		}

		return nil
	})

	// Update partials with their include sources
	for i := range result.Partials {
		for _, defName := range result.Partials[i].DefinedNames {
			if sources, ok := includeMap[defName]; ok {
				for _, src := range sources {
					// Avoid duplicates
					found := false
					for _, existing := range result.Partials[i].IncludedFrom {
						if existing == src {
							found = true
							break
						}
					}
					if !found {
						result.Partials[i].IncludedFrom = append(result.Partials[i].IncludedFrom, src)
					}
				}
			}
		}
	}

	return result, err
}

// scanPartialTemplates scans for .tpl files and extracts partial template information
func ScanPartialTemplates(templatesDir string) ([]PartialTemplate, map[string][]string) {
	var partials []PartialTemplate
	includeMap := make(map[string][]string) // template name -> files that include it

	_ = filepath.WalkDir(templatesDir, func(path string, d os.DirEntry, err error) error {
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
		content := string(data)

		// Extract defined template names
		definedNames := extractDefinedTemplateNames(content)
		if len(definedNames) == 0 {
			return nil // Not a partial with defines
		}

		// Extract .Values usages
		valuesUsages := extractAllValuesUsages(content)

		relPath, _ := filepath.Rel(templatesDir, path)
		if relPath == "" {
			relPath = filepath.Base(path)
		}

		partials = append(partials, PartialTemplate{
			FilePath:     "templates/" + relPath,
			DefinedNames: definedNames,
			ValuesUsages: valuesUsages,
		})

		return nil
	})

	return partials, includeMap
}

// extractDefinedTemplateNames extracts all template names defined via {{- define "..." }}
func extractDefinedTemplateNames(content string) []string {
	var names []string
	re := regexp.MustCompile(`\{\{-?\s*define\s+"([^"]+)"\s*-?\}\}`)
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		names = append(names, m[1])
	}
	return names
}

// extractAllValuesUsages extracts all .Values paths from template content
func extractAllValuesUsages(content string) []string {
	seen := make(map[string]bool)
	var usages []string

	re := regexp.MustCompile(`\.Values\.([a-zA-Z0-9_.]+)`)
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		path := m[1]
		if !seen[path] {
			seen[path] = true
			usages = append(usages, path)
		}
	}

	return usages
}

// extractIncludeNames extracts template names from include directives
func extractIncludeNames(content string) []string {
	var names []string
	re := regexp.MustCompile(`include\s+"([^"]+)"`)
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		names = append(names, m[1])
	}
	return names
}

// valuesPathExists checks if a dot-notation path exists in values.yaml
// Returns (exists, isArray, error)
func ValuesPathExists(chartRoot, dotPath string) (bool, bool, error) {
	valuesPath := filepath.Join(chartRoot, "values.yaml")
	data, err := os.ReadFile(valuesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, false, err
	}

	parts := strings.Split(dotPath, ".")
	node := findYAMLNodeAtPath(&doc, parts)
	if node == nil {
		return false, false, nil
	}

	return true, node.Kind == yaml.SequenceNode, nil
}

// findYAMLNodeAtPath traverses a YAML document to find the node at the given path
func findYAMLNodeAtPath(node *yaml.Node, path []string) *yaml.Node {
	if node == nil || len(path) == 0 {
		return node
	}

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) > 0 {
			return findYAMLNodeAtPath(node.Content[0], path)
		}
		return nil

	case yaml.MappingNode:
		key := path[0]
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				return findYAMLNodeAtPath(node.Content[i+1], path[1:])
			}
		}
		return nil

	default:
		return nil
	}
}

// checkCandidatesInValues updates candidates with ExistsInValues based on values.yaml
func CheckCandidatesInValues(chartRoot string, candidates []DetectedCandidate) []DetectedCandidate {
	result := make([]DetectedCandidate, len(candidates))
	for i, c := range candidates {
		exists, _, err := ValuesPathExists(chartRoot, c.ValuesPath)
		if err != nil {
			// On error, assume exists (conservative)
			c.ExistsInValues = true
		} else {
			c.ExistsInValues = exists
		}
		result[i] = c
	}
	return result
}

// convertCRDFieldInfo converts crd.FieldInfo to k8s.FieldInfo
func convertCRDFieldInfo(crdFI *crd.FieldInfo) *FieldInfo {
	if crdFI == nil {
		return nil
	}
	return &FieldInfo{
		Path:     crdFI.Path,
		MergeKey: crdFI.MergeKey,
	}
}
