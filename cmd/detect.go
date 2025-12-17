package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/crd"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/k8s"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/template"
)

func runDetect() {
	fs := flag.NewFlagSet("detect", flag.ExitOnError)
	fs.StringVar(&chartDir, "chart", ".", "path to chart root")
	fs.StringVar(&configPath, "config", "", "path to user config")
	verbose := false
	fs.BoolVar(&verbose, "v", false, "verbose output (show template files, partials, and warnings)")
	fs.BoolVar(&recursive, "recursive", false, "recursively detect in file:// subcharts")
	fs.Usage = func() {
		fmt.Print(`
Scan a Helm chart to detect arrays that can be converted to maps based on
unique key fields. This is a read-only operation that reports potential conversions
without modifying any files.

Built-in Kubernetes types (Deployment, Pod, Service, etc.) are detected automatically.
For Custom Resources (CRs), first load their CRD definitions using 'helm list-to-map load-crd'.

Usage:
  helm list-to-map detect [flags]

Flags:
      --chart string    path to chart root (default: current directory)
      --config string   path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
  -h, --help            help for detect
      --recursive       recursively detect in file:// subcharts (for umbrella charts)
  -v                    verbose output (show template files, partials, and warnings)

Examples:
  # Detect convertible fields in a chart
  helm list-to-map detect --chart ./my-chart

  # First load CRDs for Custom Resources, then detect
  helm list-to-map load-crd https://raw.githubusercontent.com/.../alertmanager-crd.yaml
  helm list-to-map detect --chart ./my-chart

  # Verbose output to see warnings and partial templates
  helm list-to-map detect --chart ./my-chart -v

  # Detect in umbrella chart and all file:// subcharts
  helm list-to-map detect --chart ./umbrella-chart --recursive
`)
	}
	_ = fs.Parse(os.Args[2:])

	root, err := findChartRoot(chartDir)
	if err != nil {
		fatal(err)
	}

	// Handle recursive detection for umbrella charts
	if recursive {
		runRecursiveDetect(root, verbose)
		return
	}

	// Load CRDs from plugin config directory
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: loading CRDs: %v\n", err)
	}

	// Use new programmatic detection via K8s API introspection
	result, err := k8s.DetectConversionCandidatesFull(root)
	if err != nil {
		fatal(err)
	}

	// Also check for user-defined rules (for CRDs)
	userDetected := scanForUserRules(root)

	// Combine both sources
	allDetected := make(map[string]k8s.DetectedCandidate)
	for _, c := range result.Candidates {
		allDetected[c.ValuesPath] = c
	}
	for _, c := range userDetected {
		if _, exists := allDetected[c.ValuesPath]; !exists {
			allDetected[c.ValuesPath] = c
		}
	}

	// Check values.yaml existence for each candidate
	var allCandidates []k8s.DetectedCandidate
	for _, c := range allDetected {
		allCandidates = append(allCandidates, c)
	}
	allCandidates = k8s.CheckCandidatesInValues(root, allCandidates)

	// Separate candidates with values vs template-only
	var withValues, templateOnly []k8s.DetectedCandidate
	for _, c := range allCandidates {
		if c.ExistsInValues {
			withValues = append(withValues, c)
		} else {
			templateOnly = append(templateOnly, c)
		}
	}

	// Print candidates with values (will be fully converted)
	if len(withValues) > 0 {
		fmt.Println("Detected convertible arrays:")
		for _, info := range withValues {
			if verbose {
				fmt.Printf("  %s\n", info.ValuesPath)
				fmt.Printf("    Key:      %s\n", info.MergeKey)
				if info.ElementType != "" {
					fmt.Printf("    Type:     %s\n", info.ElementType)
				}
				if info.TemplateFile != "" {
					fmt.Printf("    Template: %s\n", info.TemplateFile)
				}
				if info.ResourceKind != "" {
					fmt.Printf("    Resource: %s\n", info.ResourceKind)
				}
			} else {
				typeInfo := ""
				if info.ElementType != "" {
					typeInfo = fmt.Sprintf(", type=%s", info.ElementType)
				}
				fmt.Printf("  %s (key=%s%s)\n", info.ValuesPath, info.MergeKey, typeInfo)
			}
		}
	}

	// Print template-only candidates (no values.yaml entry)
	if len(templateOnly) > 0 {
		fmt.Println()
		fmt.Println("Template patterns without values.yaml entries:")
		fmt.Println("  These templates reference arrays that don't exist in values.yaml.")
		fmt.Println("  Convert will still update templates (making them map-ready).")
		fmt.Println()
		for _, info := range templateOnly {
			typeInfo := ""
			if info.ElementType != "" {
				typeInfo = fmt.Sprintf(", type=%s", info.ElementType)
			}
			fmt.Printf("  %s (key=%s%s)\n", info.ValuesPath, info.MergeKey, typeInfo)
			if verbose && info.TemplateFile != "" {
				fmt.Printf("    Template: %s\n", info.TemplateFile)
			}
		}
	}

	// Print warnings for undetected usages, grouped by category
	if len(result.Undetected) > 0 {
		// Group by category
		crdNoKeys := filterByCategory(result.Undetected, k8s.CategoryCRDNoKeys)
		k8sNoKeys := filterByCategory(result.Undetected, k8s.CategoryK8sNoKeys)
		missingCRD := filterByCategory(result.Undetected, k8s.CategoryMissingCRD)
		unknownType := filterByCategory(result.Undetected, k8s.CategoryUnknownType)

		// Arrays with known type but no merge keys (CRD or K8s)
		knownArrays := append(crdNoKeys, k8sNoKeys...)
		if len(knownArrays) > 0 {
			fmt.Println()
			fmt.Println("Arrays without auto-detected unique keys:")
			fmt.Println("  These are confirmed array fields, but lack merge key annotations.")
			fmt.Println("  Add rules if you want to convert them to maps:")
			fmt.Println()
			for _, u := range knownArrays {
				fmt.Printf("  %s (in %s:%d)\n", u.ValuesPath, u.TemplateFile, u.LineNumber)
				if verbose {
					fmt.Printf("    %s\n", u.Reason)
					addRuleCmd := fmt.Sprintf("helm list-to-map add-rule --path='%s[]' --uniqueKey=name", u.ValuesPath)
					fmt.Printf("    Add rule: %s\n", addRuleCmd)
				}
			}
			if !verbose {
				fmt.Println()
				fmt.Println("  Use -v for suggested add-rule commands.")
			}
		}

		// Missing CRDs - we don't know the type
		if len(missingCRD) > 0 {
			fmt.Println()
			fmt.Println("Fields in Custom Resources without loaded CRDs:")
			fmt.Println("  Load the CRD to determine if these are arrays:")
			fmt.Println()
			for _, u := range missingCRD {
				fmt.Printf("  %s (in %s:%d)\n", u.ValuesPath, u.TemplateFile, u.LineNumber)
				if verbose {
					fmt.Printf("    Resource: %s/%s\n", u.APIVersion, u.Kind)
				}
			}
		}

		// Unknown type - no API info at all
		if len(unknownType) > 0 {
			fmt.Println()
			fmt.Println("Fields with unknown type (may or may not be arrays):")
			fmt.Println("  These use toYaml but the resource type couldn't be determined.")
			fmt.Println("  Review manually and add rules for actual arrays:")
			fmt.Println()
			for _, u := range unknownType {
				fmt.Printf("  %s (in %s:%d)\n", u.ValuesPath, u.TemplateFile, u.LineNumber)
				if verbose {
					addRuleCmd := fmt.Sprintf("helm list-to-map add-rule --path='%s[]' --uniqueKey=name", u.ValuesPath)
					fmt.Printf("    Add rule: %s\n", addRuleCmd)
				}
			}
		}

		// Check if any detected candidates have nested list fields that users should know about
		nestedListWarnings := findNestedListFieldWarnings(result.Candidates)
		if len(nestedListWarnings) > 0 && verbose {
			fmt.Println()
			fmt.Println("Note: Some detected fields render large objects containing nested lists:")
			for _, w := range nestedListWarnings {
				fmt.Printf("  %s contains nested list fields: %s\n", w.parentPath, strings.Join(w.nestedFields, ", "))
			}
			fmt.Println("  Consider breaking these into separate values for better override granularity.")
		}

		if verbose && (len(knownArrays) > 0 || len(unknownType) > 0) {
			fmt.Println()
			fmt.Println("Tip: Replace 'name' with the actual unique key field for each array.")
		}
	}

	// Print partial templates info (verbose only)
	if verbose && len(result.Partials) > 0 {
		fmt.Println()
		fmt.Println("Partial templates:")
		for _, p := range result.Partials {
			fmt.Printf("  %s\n", p.FilePath)
			if len(p.DefinedNames) > 0 {
				fmt.Printf("    Defines: %s\n", strings.Join(p.DefinedNames, ", "))
			}
			if len(p.ValuesUsages) > 0 {
				fmt.Printf("    Values:  %s\n", strings.Join(p.ValuesUsages, ", "))
			}
			if len(p.IncludedFrom) > 0 {
				fmt.Printf("    Used by: %s\n", strings.Join(p.IncludedFrom, ", "))
			}
		}
	}

	// Collect unique Custom Resources without loaded CRDs (always shown, not just verbose)
	missingCRDs, versionMismatches := collectMissingCRDs(result.Undetected)

	// Show version mismatches first (user has CRD but wrong version)
	if len(versionMismatches) > 0 {
		fmt.Println()
		fmt.Printf("Warning: %d Custom Resource type(s) using version not in loaded CRD:\n", len(versionMismatches))
		for _, vm := range versionMismatches {
			fmt.Printf("  - %s (loaded versions: %s)\n", vm.APIVersionKind, strings.Join(vm.AvailableVersions, ", "))
		}
		fmt.Println("Download CRD with matching version or update templates to use available version.")
	}

	// Show completely missing CRDs with smart suggestions
	if len(missingCRDs) > 0 {
		// Check which missing CRDs are available in common-crds.yaml
		commonGroups := getCommonCRDGroups()
		var inCommon, notInCommon []string
		for _, cr := range missingCRDs {
			group := extractAPIGroup(cr)
			if commonGroups[group] {
				inCommon = append(inCommon, cr)
			} else {
				notInCommon = append(notInCommon, cr)
			}
		}

		fmt.Println()
		fmt.Printf("Note: %d Custom Resource type(s) found without loaded CRDs:\n", len(missingCRDs))

		if len(inCommon) > 0 {
			fmt.Println("  Available via --common:")
			for _, cr := range inCommon {
				fmt.Printf("    - %s\n", cr)
			}
			fmt.Println("  Run: helm list-to-map load-crd --common")
		}

		if len(notInCommon) > 0 {
			if len(inCommon) > 0 {
				fmt.Println()
				fmt.Println("  Requires manual download:")
			}
			for _, cr := range notInCommon {
				fmt.Printf("    - %s\n", cr)
			}
			fmt.Println("  Load with: helm list-to-map load-crd <url-or-file>")
		}
	}

	// Summary if nothing found
	if len(allDetected) == 0 && len(result.Undetected) == 0 {
		fmt.Println("No convertible lists detected.")
	}
}

// nestedListWarning represents a detected field that has nested list fields
type nestedListWarning struct {
	parentPath   string
	nestedFields []string
}

// findNestedListFieldWarnings checks detected candidates for fields that contain nested lists
// This helps users understand when they might want to break up large YAML blocks
func findNestedListFieldWarnings(candidates []k8s.DetectedCandidate) []nestedListWarning {
	var warnings []nestedListWarning

	// Known K8s types that contain nested list fields
	typesWithNestedLists := map[string][]string{
		"containers":     {"env", "volumeMounts", "ports"},
		"initContainers": {"env", "volumeMounts", "ports"},
	}

	for _, c := range candidates {
		lastSegment := c.SectionName
		if nestedFields, ok := typesWithNestedLists[lastSegment]; ok {
			warnings = append(warnings, nestedListWarning{
				parentPath:   c.ValuesPath,
				nestedFields: nestedFields,
			})
		}
	}

	return warnings
}

// CRDStatus represents the status of a CRD for a given apiVersion/kind
type CRDStatus struct {
	APIVersionKind    string   // e.g., "monitoring.coreos.com/v1/Alertmanager"
	IsMissing         bool     // CRD not loaded at all
	IsVersionMismatch bool     // CRD loaded but different version
	AvailableVersions []string // Versions available in loaded CRD (if any)
}

// filterByCategory returns undetected usages matching the given category
func filterByCategory(undetected []k8s.UndetectedUsage, category k8s.UndetectedCategory) []k8s.UndetectedUsage {
	var result []k8s.UndetectedUsage
	for _, u := range undetected {
		if u.Category == category {
			result = append(result, u)
		}
	}
	return result
}

// getCommonCRDGroups returns a set of API groups available in common-crds.yaml
func getCommonCRDGroups() map[string]bool {
	groups := make(map[string]bool)

	// Try to load common-crds.yaml from plugin directory
	pluginDir := os.Getenv("HELM_PLUGIN_DIR")
	if pluginDir == "" {
		// Fallback: try current directory (for development)
		pluginDir = "."
	}

	sourcesFile := filepath.Join(pluginDir, "common-crds.yaml")
	sources, err := crd.LoadCRDSources(sourcesFile)
	if err != nil {
		// If we can't load common-crds.yaml, return empty set
		return groups
	}

	for group := range sources {
		groups[group] = true
	}
	return groups
}

// extractAPIGroup extracts the API group from an apiVersion/kind string
// e.g., "monitoring.coreos.com/v1/ServiceMonitor" -> "monitoring.coreos.com"
func extractAPIGroup(apiVersionKind string) string {
	parts := strings.Split(apiVersionKind, "/")
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

// collectMissingCRDs extracts unique Custom Resource types that don't have loaded CRDs
// Also detects version mismatches (CRD loaded but wrong version)
func collectMissingCRDs(undetected []k8s.UndetectedUsage) (missing []string, versionMismatches []CRDStatus) {
	seenMissing := make(map[string]bool)
	seenMismatch := make(map[string]bool)

	for _, u := range undetected {
		// Only include entries that have both APIVersion and Kind (Custom Resources)
		if u.APIVersion == "" || u.Kind == "" {
			continue
		}

		// Skip built-in K8s types (they don't need CRDs)
		if k8s.ResolveKubeAPIType(u.APIVersion, u.Kind) != nil {
			continue
		}

		// Skip meta-types that are wrappers, not real resources
		if u.Kind == "List" {
			continue
		}

		key := u.APIVersion + "/" + u.Kind

		// Check if this is a version mismatch (CRD loaded but different version)
		hasGroupKind, hasVersion, availableVersions := crd.GetGlobalRegistry().CheckVersionMismatch(u.APIVersion, u.Kind)
		if hasGroupKind && !hasVersion {
			// CRD exists but with different version
			if !seenMismatch[key] {
				seenMismatch[key] = true
				versionMismatches = append(versionMismatches, CRDStatus{
					APIVersionKind:    key,
					IsVersionMismatch: true,
					AvailableVersions: availableVersions,
				})
			}
		} else if !hasGroupKind {
			// CRD not loaded at all
			if !seenMissing[key] {
				seenMissing[key] = true
				missing = append(missing, key)
			}
		}
	}

	return missing, versionMismatches
}

// scanForUserRules scans templates using user-defined rules (for CRDs)
func scanForUserRules(chartRoot string) []k8s.DetectedCandidate {
	var detected []k8s.DetectedCandidate
	seen := make(map[string]bool)

	// Only process user-defined rules (not built-in ones)
	if len(conf.Rules) == 0 {
		return detected
	}

	tdir := filepath.Join(chartRoot, "templates")

	// Regex patterns for detecting list-rendering in templates
	reToYaml := regexp.MustCompile(`\{\{-?\s*toYaml\s+\.Values\.([a-zA-Z0-9_.]+)\s*\|`)
	reWith := regexp.MustCompile(`\{\{-?\s*with\s+\.Values\.([a-zA-Z0-9_.]+)\s*\}\}`)
	reRange := regexp.MustCompile(`\{\{-?\s*range\s+.*?\.Values\.([a-zA-Z0-9_.]+)\s*\}\}`)

	_ = filepath.WalkDir(tdir, func(path string, d fs.DirEntry, err error) error {
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
		content := string(data)

		// Extract all .Values.* paths from template patterns
		paths := make(map[string]bool)
		for _, match := range reToYaml.FindAllStringSubmatch(content, -1) {
			if len(match) > 1 {
				paths[match[1]] = true
			}
		}
		for _, match := range reWith.FindAllStringSubmatch(content, -1) {
			if len(match) > 1 {
				paths[match[1]] = true
			}
		}
		for _, match := range reRange.FindAllStringSubmatch(content, -1) {
			if len(match) > 1 {
				paths[match[1]] = true
			}
		}

		// Check each extracted path against user rules
		for pathStr := range paths {
			if seen[pathStr] {
				continue
			}

			segments := strings.Split(pathStr, ".")
			rule := matchRule(segments)
			if rule == nil {
				continue
			}

			// Determine unique key
			uniqueKey := rule.UniqueKeys[0]
			for _, k := range rule.UniqueKeys {
				if k == "name" {
					uniqueKey = k
					break
				}
			}

			seen[pathStr] = true
			detected = append(detected, k8s.DetectedCandidate{
				ValuesPath:  pathStr,
				MergeKey:    uniqueKey,
				ElementType: "(user rule)",
				SectionName: getLastPathSegment(pathStr),
			})
		}

		return nil
	})

	return detected
}

// runRecursiveDetect handles the --recursive flag for detect command
// It detects convertible paths in all file:// subcharts
func runRecursiveDetect(umbrellaRoot string, verbose bool) {
	fmt.Printf("Recursive detection for umbrella chart: %s\n", umbrellaRoot)

	// Parse Chart.yaml to find file:// dependencies
	deps, err := parseChartDependencies(umbrellaRoot)
	if err != nil {
		fatal(fmt.Errorf("parsing dependencies: %w", err))
	}

	if len(deps) == 0 {
		fmt.Println("No file:// dependencies found in Chart.yaml.")
		fmt.Println("Use --recursive only for umbrella charts with local subcharts.")
		return
	}

	fmt.Printf("\nFound %d file:// subchart(s):\n", len(deps))
	for _, dep := range deps {
		fmt.Printf("  - %s (%s)\n", dep.Name, dep.Repository)
	}

	// Load CRDs from plugin config directory
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: loading CRDs: %v\n", err)
	}

	// Detect in each subchart
	totalDetected := 0
	totalSkipped := 0
	for _, dep := range deps {
		subchartPath := resolveSubchartPath(umbrellaRoot, dep.Repository)

		// Check if subchart exists
		if _, err := os.Stat(filepath.Join(subchartPath, "Chart.yaml")); err != nil {
			fmt.Fprintf(os.Stderr, "\nWarning: Subchart %s not found at %s, skipping\n", dep.Name, subchartPath)
			continue
		}

		fmt.Printf("\n=== Subchart: %s ===\n", dep.Name)

		// Detect candidates
		candidates, err := k8s.DetectConversionCandidates(subchartPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
			continue
		}

		// Also check for user-defined rules
		userDetected := scanForUserRules(subchartPath)
		candidates = append(candidates, userDetected...)

		// Check template patterns
		var pathInfos []template.PathInfo
		for _, c := range candidates {
			pathInfos = append(pathInfos, template.PathInfo{
				DotPath:     c.ValuesPath,
				MergeKey:    c.MergeKey,
				SectionName: c.SectionName,
			})
		}
		matchedPaths := template.CheckTemplatePatterns(subchartPath, pathInfos)

		// Report results
		var detected, skipped []k8s.DetectedCandidate
		for _, c := range candidates {
			if matchedPaths[c.ValuesPath] {
				detected = append(detected, c)
			} else {
				skipped = append(skipped, c)
			}
		}

		if len(detected) == 0 && len(skipped) == 0 {
			fmt.Println("  No convertible arrays detected")
			continue
		}

		// Check values.yaml existence for detected candidates
		detected = k8s.CheckCandidatesInValues(subchartPath, detected)

		// Separate by values existence
		var withValues, templateOnly []k8s.DetectedCandidate
		for _, c := range detected {
			if c.ExistsInValues {
				withValues = append(withValues, c)
			} else {
				templateOnly = append(templateOnly, c)
			}
		}

		if len(withValues) > 0 {
			fmt.Printf("  Convertible - has values (%d):\n", len(withValues))
			for _, c := range withValues {
				if verbose {
					fmt.Printf("    %s\n", c.ValuesPath)
					fmt.Printf("      Key:  %s\n", c.MergeKey)
					if c.ElementType != "" {
						fmt.Printf("      Type: %s\n", c.ElementType)
					}
				} else {
					fmt.Printf("    - %s (key=%s)\n", c.ValuesPath, c.MergeKey)
				}
			}
			totalDetected += len(withValues)
		}

		if len(templateOnly) > 0 {
			fmt.Printf("  Convertible - template only (%d):\n", len(templateOnly))
			for _, c := range templateOnly {
				fmt.Printf("    - %s (key=%s) [no value in values.yaml]\n", c.ValuesPath, c.MergeKey)
			}
			totalDetected += len(templateOnly)
		}

		if len(skipped) > 0 {
			fmt.Printf("  Skipped - unsupported template pattern (%d):\n", len(skipped))
			for _, c := range skipped {
				fmt.Printf("    - %s\n", c.ValuesPath)
			}
			totalSkipped += len(skipped)
		}
	}

	// Summary
	fmt.Println("\n=== Detection Summary ===")
	fmt.Printf("Total convertible paths: %d\n", totalDetected)
	fmt.Printf("Total skipped paths: %d\n", totalSkipped)

	if totalDetected > 0 {
		fmt.Println("\nTo convert, run:")
		fmt.Printf("  helm list-to-map convert --chart %s --recursive\n", umbrellaRoot)
	}
}
