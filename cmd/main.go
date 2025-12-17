// Build to: bin/list-to-map
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/template"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/transform"
	"gopkg.in/yaml.v3"
)

// Rule represents a user-defined conversion rule for CRDs and custom resources
type Rule struct {
	PathPattern   string   `yaml:"pathPattern"`
	UniqueKeys    []string `yaml:"uniqueKeys"`
	PromoteScalar string   `yaml:"promoteScalar,omitempty"`
}

// Config holds user-defined conversion rules
type Config struct {
	Rules              []Rule `yaml:"rules"`
	LastWinsDuplicates bool   `yaml:"lastWinsDuplicates"`
	SortKeys           bool   `yaml:"sortKeys"`
}

// PathInfo is now in pkg/template
type PathInfo = template.PathInfo

// ArrayEdit is now in pkg/transform
type ArrayEdit = transform.ArrayEdit

var (
	subcmd           string
	chartDir         string
	dryRun           bool
	backupExt        string
	configPath       string
	recursive        bool
	conf             Config
	transformedPaths []PathInfo
)

// SubchartConversion tracks what was converted in a subchart
type SubchartConversion struct {
	Name           string     // Subchart name (used as prefix in umbrella values)
	ConvertedPaths []PathInfo // Paths that were converted
}

// ChartDependency represents a dependency from Chart.yaml
type ChartDependency struct {
	Name       string `yaml:"name"`
	Repository string `yaml:"repository"`
	Condition  string `yaml:"condition,omitempty"`
}

// ChartYAML represents the relevant parts of Chart.yaml
type ChartYAML struct {
	Dependencies []ChartDependency `yaml:"dependencies"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	subcmd = os.Args[1]

	// Handle top-level help
	if subcmd == "-h" || subcmd == "--help" {
		usage()
		return
	}

	// Load user-defined rules for CRDs and custom resources
	if configPath == "" {
		configPath = defaultUserConfigPath()
	}
	if b, err := os.ReadFile(configPath); err == nil {
		_ = yaml.Unmarshal(b, &conf)
	}

	switch subcmd {
	case "detect":
		runDetect()
	case "convert":
		runConvert()
	case "add-rule":
		runAddRule()
	case "rules":
		runListRules()
	case "load-crd":
		runLoadCRD()
	case "list-crds":
		runListCRDs()
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q for \"helm list-to-map\"\n", subcmd)
		fmt.Fprintf(os.Stderr, "Run 'helm list-to-map --help' for usage.\n")
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`
The list-to-map plugin converts Helm chart array values with unique keys into
map-based configurations, making values easier to override and reducing duplication.

Usage:
  helm list-to-map [command] [flags]

Available Commands:
  detect      scan values.yaml and report convertible arrays
  convert     transform values.yaml and update templates
  load-crd    load CRD definitions for Custom Resource support
  list-crds   list loaded CRD types and their convertible fields
  add-rule    add a custom conversion rule to your config
  rules       list all active rules (built-in + custom)

Flags:
  -h, --help   help for list-to-map

IMPORTANT - Ordering Limitation:
  Map-based values are rendered in alphabetical order (sorted by key).
  For environment variables, this means $(VAR) references to other env vars
  may not work if the referenced var comes later alphabetically.

  Example that will BREAK after conversion:
    env:
      API_URL:           # "A" comes before "B"
        value: "$(BASE_URL)/api"  # References BASE_URL
      BASE_URL:          # Defined AFTER API_URL alphabetically
        value: "https://example.com"

  Ensure your env vars don't rely on definition order, or keep them as arrays.

Use "helm list-to-map [command] --help" for more information about a command.
`)
}

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
	result, err := detectConversionCandidatesFull(root)
	if err != nil {
		fatal(err)
	}

	// Also check for user-defined rules (for CRDs)
	userDetected := scanForUserRules(root)

	// Combine both sources
	allDetected := make(map[string]DetectedCandidate)
	for _, c := range result.Candidates {
		allDetected[c.ValuesPath] = c
	}
	for _, c := range userDetected {
		if _, exists := allDetected[c.ValuesPath]; !exists {
			allDetected[c.ValuesPath] = c
		}
	}

	// Check values.yaml existence for each candidate
	var allCandidates []DetectedCandidate
	for _, c := range allDetected {
		allCandidates = append(allCandidates, c)
	}
	allCandidates = checkCandidatesInValues(root, allCandidates)

	// Separate candidates with values vs template-only
	var withValues, templateOnly []DetectedCandidate
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
		crdNoKeys := filterByCategory(result.Undetected, CategoryCRDNoKeys)
		k8sNoKeys := filterByCategory(result.Undetected, CategoryK8sNoKeys)
		missingCRD := filterByCategory(result.Undetected, CategoryMissingCRD)
		unknownType := filterByCategory(result.Undetected, CategoryUnknownType)

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
func findNestedListFieldWarnings(candidates []DetectedCandidate) []nestedListWarning {
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
func filterByCategory(undetected []UndetectedUsage, category UndetectedCategory) []UndetectedUsage {
	var result []UndetectedUsage
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
	sources, err := LoadCRDSources(sourcesFile)
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
func collectMissingCRDs(undetected []UndetectedUsage) (missing []string, versionMismatches []CRDStatus) {
	seenMissing := make(map[string]bool)
	seenMismatch := make(map[string]bool)

	for _, u := range undetected {
		// Only include entries that have both APIVersion and Kind (Custom Resources)
		if u.APIVersion == "" || u.Kind == "" {
			continue
		}

		// Skip built-in K8s types (they don't need CRDs)
		if resolveKubeAPIType(u.APIVersion, u.Kind) != nil {
			continue
		}

		// Skip meta-types that are wrappers, not real resources
		if u.Kind == "List" {
			continue
		}

		key := u.APIVersion + "/" + u.Kind

		// Check if this is a version mismatch (CRD loaded but different version)
		hasGroupKind, hasVersion, availableVersions := globalCRDRegistry.CheckVersionMismatch(u.APIVersion, u.Kind)
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
func scanForUserRules(chartRoot string) []DetectedCandidate {
	var detected []DetectedCandidate
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
			detected = append(detected, DetectedCandidate{
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

func runConvert() {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	fs.StringVar(&chartDir, "chart", ".", "path to chart root")
	fs.StringVar(&configPath, "config", "", "path to user config")
	fs.BoolVar(&dryRun, "dry-run", false, "preview changes without writing files")
	fs.StringVar(&backupExt, "backup-ext", ".bak", "backup file extension")
	fs.BoolVar(&recursive, "recursive", false, "recursively convert file:// subcharts and update umbrella values")
	fs.Usage = func() {
		fmt.Print(`
Transform array-based configurations to map-based configurations in values.yaml
and automatically update corresponding template files. This command modifies files
in place, creating backups with the specified extension.

The conversion process:
  1. Scans templates using K8s API introspection and CRD schemas
  2. Identifies list fields with required unique keys (patchMergeKey or x-kubernetes-list-map-keys)
  3. Converts matching arrays to maps using unique key fields
  4. Updates template files to use new helper functions
  5. Generates helper templates if they don't exist

Built-in Kubernetes types are detected automatically. For Custom Resources (CRs),
first load their CRD definitions using 'helm list-to-map load-crd'.

Usage:
  helm list-to-map convert [flags]

Flags:
      --backup-ext string   backup file extension (default: ".bak")
      --chart string        path to chart root (default: current directory)
      --config string       path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
      --dry-run             preview changes without writing files
  -h, --help                help for convert
      --recursive           recursively convert file:// subcharts and update umbrella values

Examples:
  # Convert a chart with built-in K8s types
  helm list-to-map convert --chart ./my-chart

  # First load CRDs for Custom Resources, then convert
  helm list-to-map load-crd https://raw.githubusercontent.com/.../alertmanager-crd.yaml
  helm list-to-map convert --chart ./my-chart

  # Preview changes without modifying files
  helm list-to-map convert --dry-run

  # Convert umbrella chart and all file:// subcharts recursively
  helm list-to-map convert --chart ./umbrella-chart --recursive
`)
	}
	_ = fs.Parse(os.Args[2:])

	root, err := findChartRoot(chartDir)
	if err != nil {
		fatal(err)
	}

	// Handle recursive conversion of umbrella charts
	if recursive {
		runRecursiveConvert(root)
		return
	}

	// Load CRDs from plugin config directory
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: loading CRDs: %v\n", err)
	}

	// Use new programmatic detection via K8s API introspection
	candidates, err := detectConversionCandidates(root)
	if err != nil {
		fatal(err)
	}

	// Also check for user-defined rules (for CRDs)
	userDetected := scanForUserRules(root)
	candidates = append(candidates, userDetected...)

	// Build PathInfo list and check which paths have matching template patterns
	var pathInfos []PathInfo
	for _, c := range candidates {
		pathInfos = append(pathInfos, PathInfo{
			DotPath:     c.ValuesPath,
			MergeKey:    c.MergeKey,
			SectionName: c.SectionName,
		})
	}

	// Check template patterns BEFORE converting values
	// Only convert values for paths where template patterns actually match
	matchedPaths := template.CheckTemplatePatterns(root, pathInfos)

	// Filter candidates to only include paths with matching template patterns
	candidateMap := make(map[string]DetectedCandidate)
	var skippedPaths []string
	for _, c := range candidates {
		if matchedPaths[c.ValuesPath] {
			candidateMap[c.ValuesPath] = c
		} else {
			skippedPaths = append(skippedPaths, c.ValuesPath)
		}
	}

	// Check values.yaml existence for candidates with matching templates
	var candidateList []DetectedCandidate
	for _, c := range candidateMap {
		candidateList = append(candidateList, c)
	}
	candidateList = checkCandidatesInValues(root, candidateList)

	// Separate by values existence
	var withValuesCandidates, templateOnlyCandidates []DetectedCandidate
	for _, c := range candidateList {
		if c.ExistsInValues {
			withValuesCandidates = append(withValuesCandidates, c)
		} else {
			templateOnlyCandidates = append(templateOnlyCandidates, c)
		}
	}

	// Rebuild candidateMap with only candidates that have values
	candidateMap = make(map[string]DetectedCandidate)
	for _, c := range withValuesCandidates {
		candidateMap[c.ValuesPath] = c
	}

	// Warn about paths that couldn't be converted
	if len(skippedPaths) > 0 {
		fmt.Println("\nSkipped (template pattern not supported):")
		for _, p := range skippedPaths {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println("  These paths use inline append patterns (e.g., static entries + toYaml)")
		fmt.Println("  that cannot be automatically converted.")
	}

	valuesPath := filepath.Join(root, "values.yaml")
	doc, raw, err := loadValuesNode(valuesPath)
	if err != nil {
		fatal(err)
	}

	// Use line-based editing to preserve original formatting
	var edits []ArrayEdit
	transform.FindArrayEdits(doc, nil, candidateMap, &edits)

	// Track all backup files created
	var backupFiles []string

	if len(edits) > 0 {
		out := transform.ApplyLineEdits(raw, edits)

		if dryRun {
			fmt.Println("=== values.yaml (updated preview) ===")
			fmt.Println(string(out))
		} else {
			backupPath := valuesPath + backupExt
			if err := backupFile(valuesPath, backupExt, raw); err != nil {
				fatal(err)
			}
			backupFiles = append(backupFiles, backupPath)
			if err := os.WriteFile(valuesPath, out, 0644); err != nil {
				fatal(err)
			}
		}

		// Check if any env vars are being converted
		hasEnvVars := false
		for _, edit := range edits {
			if strings.Contains(edit.Candidate.ElementType, "EnvVar") ||
				strings.HasSuffix(edit.Candidate.ValuesPath, ".env") ||
				strings.HasSuffix(edit.Candidate.ValuesPath, "Env") {
				hasEnvVars = true
				break
			}
		}

		// Report changes with detailed info
		fmt.Println("\nConverted values.yaml fields:")
		for _, edit := range edits {
			// Build JSONPath for display
			jsonPath := edit.Candidate.YAMLPath
			if edit.Candidate.ResourceKind != "" {
				jsonPath = edit.Candidate.ResourceKind + "." + jsonPath
			}

			// Count items
			itemCount := 0
			transform.WalkForCount(doc, edit.Candidate.ValuesPath, &itemCount)

			// Display detailed info
			fmt.Printf("  %s:\n", edit.Candidate.ValuesPath)
			fmt.Printf("    JSONPath: %s\n", jsonPath)
			fmt.Printf("    Key:      %s\n", edit.Candidate.MergeKey)
			if edit.Candidate.ElementType != "" {
				fmt.Printf("    Type:     %s\n", edit.Candidate.ElementType)
			}
			if edit.Candidate.TemplateFile != "" {
				fmt.Printf("    Used in:  templates/%s\n", edit.Candidate.TemplateFile)
			}
			if itemCount == 0 {
				fmt.Printf("    Items:    0 (empty array)\n")
			} else {
				fmt.Printf("    Items:    %d\n", itemCount)
			}

			transformedPaths = append(transformedPaths, PathInfo{
				DotPath:     edit.Candidate.ValuesPath,
				MergeKey:    edit.Candidate.MergeKey,
				SectionName: edit.Candidate.SectionName,
			})
		}

		// Warn about env var ordering if applicable
		if hasEnvVars {
			fmt.Println("\n  WARNING: Environment variables will be rendered in alphabetical order.")
			fmt.Println("  If any env var uses $(OTHER_VAR) syntax to reference another env var,")
			fmt.Println("  ensure the referenced var comes BEFORE it alphabetically, or the")
			fmt.Println("  reference will fail. See 'helm list-to-map --help' for details.")
		}
	} else {
		fmt.Println("No changes needed in values.yaml.")
	}

	// Add template-only candidates to transformedPaths for template rewriting
	if len(templateOnlyCandidates) > 0 {
		fmt.Println("\nTemplate-only conversions (no values.yaml entry):")
		for _, c := range templateOnlyCandidates {
			fmt.Printf("  %s (key=%s)\n", c.ValuesPath, c.MergeKey)
			transformedPaths = append(transformedPaths, PathInfo{
				DotPath:     c.ValuesPath,
				MergeKey:    c.MergeKey,
				SectionName: c.SectionName,
			})
		}
		fmt.Println("\n  NOTE: These templates will be updated to use map-style syntax.")
		fmt.Println("  Please manually update any comments in values.yaml or documentation")
		fmt.Println("  that describe these fields to use map format instead of list format.")
	}

	var tchanges []string
	var helperCreated bool
	if !dryRun {
		var err error
		tchanges, backupFiles, err = template.RewriteTemplatesWithBackups(root, transformedPaths, backupExt, backupFiles)
		if err != nil {
			fatal(err)
		}

		if len(tchanges) > 0 {
			fmt.Println("\nUpdated templates:")
			for _, ch := range tchanges {
				fmt.Printf("  %s\n", ch)
			}
		}

		helperCreated = template.EnsureHelpersWithReport(root)
		if helperCreated {
			fmt.Println("\nCreated helper template:")
			fmt.Printf("  templates/_listmap.tpl\n")
		}
	} else if len(transformedPaths) > 0 {
		fmt.Println("\nTemplate changes (dry-run, not applied):")
		for _, p := range transformedPaths {
			fmt.Printf("  Would update templates using .Values.%s\n", p.DotPath)
		}
		fmt.Println("  Would create templates/_listmap.tpl (if not exists)")
	}

	// Report backup files
	if !dryRun && len(backupFiles) > 0 {
		fmt.Println("\nBackup files created:")
		for _, bf := range backupFiles {
			relPath, _ := filepath.Rel(root, bf)
			if relPath == "" {
				relPath = bf
			}
			fmt.Printf("  %s\n", relPath)
		}
	}

	if len(edits) == 0 && len(tchanges) == 0 && len(templateOnlyCandidates) == 0 && !dryRun {
		fmt.Println("Nothing to convert.")
	}
}

func runAddRule() {
	path := ""
	uniqueKey := ""
	fs := flag.NewFlagSet("add-rule", flag.ExitOnError)
	fs.StringVar(&path, "path", "", "dot path to array (end with []), e.g. database.primary.extraEnv[]")
	fs.StringVar(&uniqueKey, "uniqueKey", "", "unique key field, e.g. name")
	fs.StringVar(&configPath, "config", "", "path to user config")
	fs.Usage = func() {
		fmt.Print(`
Add a custom conversion rule to your user configuration file.

Use this for:
  - CRDs that don't define x-kubernetes-list-map-keys in their OpenAPI schema
  - Custom resources without available CRD definitions
  - Any list field you want to convert that isn't auto-detected

Usage:
  helm list-to-map add-rule [flags]

Flags:
      --config string      path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
  -h, --help               help for add-rule
      --path string        dot path to array (end with []), e.g. database.primary.extraEnv[]
      --uniqueKey string   unique key field, e.g. name

Examples:
  helm list-to-map add-rule --path='istio.virtualService.http[]' --uniqueKey=name
  helm list-to-map add-rule --path='myapp.listeners[]' --uniqueKey=port
`)
	}
	_ = fs.Parse(os.Args[2:])

	if path == "" || uniqueKey == "" {
		fmt.Fprintln(os.Stderr, "Error: --path and --uniqueKey are required")
		fmt.Fprintln(os.Stderr, "Run 'helm list-to-map add-rule --help' for usage.")
		os.Exit(1)
	}

	r := Rule{PathPattern: path, UniqueKeys: []string{uniqueKey}}
	user := defaultUserConfigPath()
	if err := os.MkdirAll(filepath.Dir(user), 0755); err != nil {
		fatal(err)
	}
	var current Config
	if b, err := os.ReadFile(user); err == nil {
		_ = yaml.Unmarshal(b, &current)
	}
	current.Rules = append(current.Rules, r)
	out, _ := yaml.Marshal(current)
	if err := os.WriteFile(user, out, 0644); err != nil {
		fatal(err)
	}
	fmt.Printf("Added rule to %s: %s (key=%s)\n", user, path, uniqueKey)
}

func runListRules() {
	// Check for help flag
	for _, arg := range os.Args[2:] {
		if arg == "-h" || arg == "--help" {
			fmt.Print(`
List custom conversion rules for CRDs and custom resources.

Note: Built-in K8s types are detected automatically via API introspection
and do not require rules. Use 'detect' to see what will be converted.

Usage:
  helm list-to-map rules [flags]

Flags:
  -h, --help   help for rules
`)
			return
		}
	}

	if len(conf.Rules) == 0 {
		fmt.Println("No custom rules defined.")
		fmt.Println("Built-in K8s types are detected automatically via API introspection.")
		return
	}

	fmt.Println("Custom rules:")
	for _, r := range conf.Rules {
		fmt.Printf("- %s (key=%s)\n", r.PathPattern, r.UniqueKeys[0])
	}
}

// Global flag for force overwrite (set by runLoadCRD)
var forceOverwrite bool

func runLoadCRD() {
	fs := flag.NewFlagSet("load-crd", flag.ExitOnError)
	fs.BoolVar(&forceOverwrite, "force", false, "overwrite existing CRD files")
	loadCommon := false
	fs.BoolVar(&loadCommon, "common", false, "load CRDs from bundled crd-sources.yaml")
	fs.Usage = func() {
		fmt.Print(`
Load CRD (Custom Resource Definition) files to enable detection of convertible
fields in Custom Resources. CRDs are stored in the plugin's config directory
and automatically loaded when running 'detect' or 'convert'.

The plugin extracts x-kubernetes-list-type and x-kubernetes-list-map-keys
annotations from the CRD's OpenAPI schema to identify convertible list fields.

CRD files are named using the pattern {group}_{plural}_{storageVersion}.yaml,
so different storage versions of the same CRD coexist without overwriting.
Existing files are preserved unless --force is used.

Usage:
  helm list-to-map load-crd [flags] <source> [source...]
  helm list-to-map load-crd --common

Arguments:
  source    CRD file path, directory, or URL (can specify multiple)

Flags:
      --common  load CRDs from bundled crd-sources.yaml (uses 'main' branch)
      --force   overwrite existing CRD files with same storage version
  -h, --help    help for load-crd

Examples:
  # Load CRD from a local file
  helm list-to-map load-crd ./alertmanager-crd.yaml

  # Load CRD from a URL
  helm list-to-map load-crd https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_alertmanagers.yaml

  # Load all CRDs from a directory (recursively)
  helm list-to-map load-crd ./my-chart/crds/

  # Load bundled common CRDs (from crd-sources.yaml)
  helm list-to-map load-crd --common

  # Force overwrite existing CRDs
  helm list-to-map load-crd --force ./crds/
`)
	}
	_ = fs.Parse(os.Args[2:])

	// Handle --common flag
	if loadCommon {
		loadCommonCRDs()
		return
	}

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Error: at least one CRD source is required (or use --common)")
		fmt.Fprintln(os.Stderr, "Run 'helm list-to-map load-crd --help' for usage.")
		os.Exit(1)
	}

	// Ensure CRD config directory exists
	crdsDir := crdConfigDir()
	if err := os.MkdirAll(crdsDir, 0755); err != nil {
		fatal(fmt.Errorf("creating CRD directory: %w", err))
	}

	// Process each source
	for _, source := range fs.Args() {
		if err := loadAndStoreCRD(source, crdsDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", source, err)
			continue
		}
	}
}

// loadCommonCRDs loads CRDs from the bundled common-crds.yaml file
func loadCommonCRDs() {
	// Find common-crds.yaml in plugin directory
	pluginDir := os.Getenv("HELM_PLUGIN_DIR")
	if pluginDir == "" {
		// Fallback: check current directory and parent
		candidates := []string{"common-crds.yaml", "../common-crds.yaml"}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				pluginDir = filepath.Dir(c)
				break
			}
		}
	}

	sourcesFile := filepath.Join(pluginDir, "common-crds.yaml")
	if _, err := os.Stat(sourcesFile); err != nil {
		// Try current directory as fallback
		sourcesFile = "common-crds.yaml"
	}

	sources, err := LoadCRDSources(sourcesFile)
	if err != nil {
		fatal(fmt.Errorf("loading common-crds.yaml: %w", err))
	}

	// Ensure CRD config directory exists
	crdsDir := crdConfigDir()
	if err := os.MkdirAll(crdsDir, 0755); err != nil {
		fatal(fmt.Errorf("creating CRD directory: %w", err))
	}

	fmt.Printf("Loading CRDs from bundled sources...\n\n")

	loaded := 0
	skipped := 0

	for group, entry := range sources {
		// Use entry's default_version, fallback to "main" if not specified
		version := entry.DefaultVersion
		if version == "" {
			version = "main"
		}

		url := entry.GetDownloadURL(version)
		if url == "" {
			if entry.Note != "" {
				fmt.Printf("  %s: skipped (%s)\n", group, entry.Note)
			} else {
				fmt.Printf("  %s: skipped (no direct URL, only url_pattern available)\n", group)
			}
			skipped++
			continue
		}

		fmt.Printf("  %s (version: %s)\n", group, version)
		fmt.Printf("    Source: %s\n", url)

		if err := loadAndStoreCRDFromURL(url, crdsDir); err != nil {
			fmt.Printf("    Error: %v\n", err)
			continue
		}
		loaded++
	}

	fmt.Printf("\nLoaded %d source(s), skipped %d\n", loaded, skipped)

	// Show what's now loaded
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	types := globalCRDRegistry.ListTypes()
	if len(types) > 0 {
		fmt.Printf("\nTotal CRD types available: %d\n", len(types))
	}
}

// loadAndStoreCRD loads a CRD from file, directory, or URL and stores it in the config directory
func loadAndStoreCRD(source, crdsDir string) error {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		// Download from URL
		return loadAndStoreCRDFromURL(source, crdsDir)
	}

	// Check if source is a directory
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("accessing source: %w", err)
	}

	if info.IsDir() {
		// Load all CRD files from directory
		return loadAndStoreCRDsFromDirectory(source, crdsDir)
	}

	// Load single file
	return loadAndStoreCRDFromFile(source, crdsDir)
}

// loadAndStoreCRDFromURL downloads a CRD from a URL and stores it
func loadAndStoreCRDFromURL(url, crdsDir string) error {
	resp, err := http.Get(url) //nolint:gosec // User-provided URL is intentional
	if err != nil {
		return fmt.Errorf("fetching URL: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	// Extract canonical filename from CRD metadata (includes storage version)
	filename, err := ExtractCanonicalFilename(data)
	if err != nil {
		// Fallback to URL-based filename
		parts := strings.Split(url, "/")
		filename = parts[len(parts)-1]
		if filename == "" || !strings.HasSuffix(filename, ".yaml") {
			filename = "crd-" + fmt.Sprintf("%d", len(url)%10000) + ".yaml"
		}
	}

	destPath := filepath.Join(crdsDir, filename)

	// Check if file exists (skip unless --force)
	if exists, reason := CRDFileExists(destPath); exists && !forceOverwrite {
		fmt.Printf("Skipped: %s -> %s (%s)\n", url, destPath, reason)
		return nil
	}

	// Write to config directory
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("writing to config: %w", err)
	}

	fmt.Printf("Loaded: %s -> %s\n", url, destPath)
	return nil
}

// loadAndStoreCRDFromFile loads a CRD from a file and stores it
func loadAndStoreCRDFromFile(source, crdsDir string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	// Extract canonical filename from CRD metadata (includes storage version)
	// This also validates that the file contains a valid CRD
	filename, err := ExtractCanonicalFilename(data)
	if err != nil {
		return fmt.Errorf("not a valid CRD: %w", err)
	}

	destPath := filepath.Join(crdsDir, filename)

	// Check if file exists (skip unless --force)
	if exists, reason := CRDFileExists(destPath); exists && !forceOverwrite {
		fmt.Printf("Skipped: %s -> %s (%s)\n", source, destPath, reason)
		return nil
	}

	// Write to config directory
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("writing to config: %w", err)
	}

	fmt.Printf("Loaded: %s -> %s\n", source, destPath)
	return nil
}

// loadAndStoreCRDsFromDirectory loads all CRD YAML files from a directory
func loadAndStoreCRDsFromDirectory(sourceDir, crdsDir string) error {
	var loaded, skipped int
	err := filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		// Try to load each YAML file as a CRD
		// Files that aren't valid CRDs are silently skipped
		if err := loadAndStoreCRDFromFile(path, crdsDir); err != nil {
			skipped++
			return nil
		}
		loaded++
		return nil
	})

	if err != nil {
		return err
	}

	if loaded == 0 {
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "Warning: no CRD files found in %s (%d YAML file(s) checked but none contained CRDs)\n", sourceDir, skipped)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: no YAML files found in %s\n", sourceDir)
		}
	} else {
		fmt.Printf("\nLoaded %d CRD file(s) from %s\n", loaded, sourceDir)
	}

	return nil
}

func runListCRDs() {
	fs := flag.NewFlagSet("list-crds", flag.ExitOnError)
	verbose := false
	fs.BoolVar(&verbose, "v", false, "show all convertible fields for each CRD")
	fs.Usage = func() {
		fmt.Print(`
List all loaded CRD types and their convertible fields.

Usage:
  helm list-to-map list-crds [flags]

Flags:
  -h, --help   help for list-crds
  -v           verbose - show all convertible fields for each CRD
`)
	}
	_ = fs.Parse(os.Args[2:])

	// Load CRDs from config
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	types := globalCRDRegistry.ListTypes()
	if len(types) == 0 {
		fmt.Println("No CRDs loaded.")
		fmt.Println("Use 'helm list-to-map load-crd <file-or-url>' to load CRD definitions.")
		return
	}

	if verbose {
		// Verbose: show each CRD with its fields
		fmt.Printf("Loaded CRD types (%d):\n", len(types))
		for _, t := range types {
			fields := globalCRDRegistry.fields[t]
			fmt.Printf("\n%s (%d fields)\n", t, len(fields))
			for _, f := range fields {
				keys := strings.Join(f.MapKeys, ", ")
				fmt.Printf("  · %s (key: %s)\n", f.Path, keys)
			}
		}
	} else {
		// Compact: table format
		// Find max CRD name length for alignment
		maxLen := len("CRD Type")
		for _, t := range types {
			if len(t) > maxLen {
				maxLen = len(t)
			}
		}

		// Print table header
		fmt.Printf("Loaded %d CRD type(s):\n\n", len(types))
		fmt.Printf("%-*s  %s\n", maxLen, "CRD Type", "Convertible Fields")
		fmt.Printf("%-*s  %s\n", maxLen, strings.Repeat("-", maxLen), "------------------")

		// Print rows
		for _, t := range types {
			fields := globalCRDRegistry.fields[t]
			fmt.Printf("%-*s  %d\n", maxLen, t, len(fields))
		}

		fmt.Println("\nUse -v to see field details.")
	}
}

func defaultUserConfigPath() string {
	home := os.Getenv("HELM_CONFIG_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".config", "helm")
	}
	return filepath.Join(home, "list-to-map", "config.yaml")
}

// crdConfigDir returns the path to the plugin's CRD storage directory
func crdConfigDir() string {
	home := os.Getenv("HELM_CONFIG_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".config", "helm")
	}
	return filepath.Join(home, "list-to-map", "crds")
}

// loadCRDsFromConfig loads all CRD definitions from the plugin's config directory
func loadCRDsFromConfig() error {
	crdsDir := crdConfigDir()
	if info, err := os.Stat(crdsDir); err != nil || !info.IsDir() {
		// No CRDs directory - that's fine, just skip
		return nil
	}

	return globalCRDRegistry.LoadFromDirectory(crdsDir)
}

func findChartRoot(start string) (string, error) {
	p := start
	for {
		if _, err := os.Stat(filepath.Join(p, "Chart.yaml")); err == nil {
			return p, nil
		}
		np := filepath.Dir(p)
		if np == p {
			break
		}
		p = np
	}
	return "", fmt.Errorf("chart.yaml not found starting from %s", start)
}

// parseChartDependencies reads Chart.yaml and returns file:// dependencies
func parseChartDependencies(chartRoot string) ([]ChartDependency, error) {
	chartPath := filepath.Join(chartRoot, "Chart.yaml")
	data, err := os.ReadFile(chartPath)
	if err != nil {
		return nil, fmt.Errorf("reading Chart.yaml: %w", err)
	}

	var chart ChartYAML
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return nil, fmt.Errorf("parsing Chart.yaml: %w", err)
	}

	// Filter to only file:// dependencies
	var fileDeps []ChartDependency
	for _, dep := range chart.Dependencies {
		if strings.HasPrefix(dep.Repository, "file://") {
			fileDeps = append(fileDeps, dep)
		}
	}

	return fileDeps, nil
}

// resolveSubchartPath resolves a file:// repository reference to an absolute path
func resolveSubchartPath(umbrellaRoot, repository string) string {
	// Remove file:// prefix
	relPath := strings.TrimPrefix(repository, "file://")
	// Resolve relative to umbrella chart root
	return filepath.Join(umbrellaRoot, relPath)
}

// convertSubchartAndTrack converts a subchart and returns the converted paths
func convertSubchartAndTrack(subchartPath string) (*SubchartConversion, error) {
	// Reset global transformedPaths before conversion
	transformedPaths = nil

	// Load CRDs from plugin config directory
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: loading CRDs: %v\n", err)
	}

	// Use programmatic detection via K8s API introspection
	candidates, err := detectConversionCandidates(subchartPath)
	if err != nil {
		return nil, fmt.Errorf("detecting candidates: %w", err)
	}

	// Also check for user-defined rules (for CRDs)
	userDetected := scanForUserRules(subchartPath)
	candidates = append(candidates, userDetected...)

	// Build PathInfo list and check which paths have matching template patterns
	var pathInfos []PathInfo
	for _, c := range candidates {
		pathInfos = append(pathInfos, PathInfo{
			DotPath:     c.ValuesPath,
			MergeKey:    c.MergeKey,
			SectionName: c.SectionName,
		})
	}

	// Check template patterns BEFORE converting values
	matchedPaths := template.CheckTemplatePatterns(subchartPath, pathInfos)

	// Filter candidates to only include paths with matching template patterns
	candidateMap := make(map[string]DetectedCandidate)
	for _, c := range candidates {
		if matchedPaths[c.ValuesPath] {
			candidateMap[c.ValuesPath] = c
		}
	}

	valuesPath := filepath.Join(subchartPath, "values.yaml")
	doc, raw, err := loadValuesNode(valuesPath)
	if err != nil {
		return nil, fmt.Errorf("loading values.yaml: %w", err)
	}

	// Use line-based editing to preserve original formatting
	var edits []ArrayEdit
	transform.FindArrayEdits(doc, nil, candidateMap, &edits)

	if len(edits) > 0 {
		out := transform.ApplyLineEdits(raw, edits)

		if !dryRun {
			backupPath := valuesPath + backupExt
			if err := backupFile(valuesPath, backupExt, raw); err != nil {
				return nil, fmt.Errorf("backing up values.yaml: %w", err)
			}
			fmt.Printf("    Backup: %s\n", backupPath)
			if err := os.WriteFile(valuesPath, out, 0644); err != nil {
				return nil, fmt.Errorf("writing values.yaml: %w", err)
			}
		}

		// Track converted paths
		for _, edit := range edits {
			transformedPaths = append(transformedPaths, PathInfo{
				DotPath:     edit.Candidate.ValuesPath,
				MergeKey:    edit.Candidate.MergeKey,
				SectionName: edit.Candidate.SectionName,
			})
		}
	}

	// Rewrite templates
	if !dryRun && len(transformedPaths) > 0 {
		tchanges, _, err := template.RewriteTemplatesWithBackups(subchartPath, transformedPaths, backupExt, nil)
		if err != nil {
			return nil, fmt.Errorf("rewriting templates: %w", err)
		}
		for _, ch := range tchanges {
			fmt.Printf("    Updated template: %s\n", ch)
		}

		// Create helper template
		if template.EnsureHelpersWithReport(subchartPath) {
			fmt.Printf("    Created: templates/_listmap.tpl\n")
		}
	}

	// Return conversion info
	chartName := filepath.Base(subchartPath)
	return &SubchartConversion{
		Name:           chartName,
		ConvertedPaths: transformedPaths,
	}, nil
}

// updateUmbrellaValues updates the umbrella chart's values.yaml to convert arrays to maps
// for paths that were converted in subcharts
func updateUmbrellaValues(umbrellaRoot string, conversions []SubchartConversion) error {
	valuesPath := filepath.Join(umbrellaRoot, "values.yaml")
	doc, raw, err := loadValuesNode(valuesPath)
	if err != nil {
		return fmt.Errorf("loading umbrella values.yaml: %w", err)
	}

	// Build a map of subchart prefixed paths to their conversion info
	// e.g., "judge-api.deployment.env" -> PathInfo{MergeKey: "name", ...}
	subchartPaths := make(map[string]PathInfo)
	for _, conv := range conversions {
		for _, p := range conv.ConvertedPaths {
			// Prefix with subchart name
			prefixedPath := conv.Name + "." + p.DotPath
			subchartPaths[prefixedPath] = p
		}
	}

	// Find arrays in umbrella values that match subchart converted paths
	candidateMap := make(map[string]DetectedCandidate)
	for path, info := range subchartPaths {
		candidateMap[path] = DetectedCandidate{
			ValuesPath:  path,
			MergeKey:    info.MergeKey,
			SectionName: info.SectionName,
		}
	}

	// Find array edits in umbrella values
	var edits []ArrayEdit
	transform.FindArrayEdits(doc, nil, candidateMap, &edits)

	if len(edits) == 0 {
		fmt.Println("\nNo umbrella values.yaml updates needed.")
		return nil
	}

	// Apply edits
	out := transform.ApplyLineEdits(raw, edits)

	if dryRun {
		fmt.Println("\n=== Umbrella values.yaml updates (dry-run) ===")
		for _, edit := range edits {
			fmt.Printf("  Would convert: %s\n", edit.Candidate.ValuesPath)
		}
	} else {
		backupPath := valuesPath + backupExt
		if err := backupFile(valuesPath, backupExt, raw); err != nil {
			return fmt.Errorf("backing up umbrella values.yaml: %w", err)
		}
		if err := os.WriteFile(valuesPath, out, 0644); err != nil {
			return fmt.Errorf("writing umbrella values.yaml: %w", err)
		}

		fmt.Println("\nUpdated umbrella values.yaml:")
		fmt.Printf("  Backup: %s\n", backupPath)
		for _, edit := range edits {
			fmt.Printf("  Converted: %s (key=%s)\n", edit.Candidate.ValuesPath, edit.Candidate.MergeKey)
		}
	}

	return nil
}

// runRecursiveConvert handles the --recursive flag for umbrella charts
// It converts all file:// subcharts and then updates the umbrella values.yaml
func runRecursiveConvert(umbrellaRoot string) {
	fmt.Printf("Recursive conversion for umbrella chart: %s\n", umbrellaRoot)

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

	// Convert each subchart
	var conversions []SubchartConversion
	for _, dep := range deps {
		subchartPath := resolveSubchartPath(umbrellaRoot, dep.Repository)

		// Check if subchart exists
		if _, err := os.Stat(filepath.Join(subchartPath, "Chart.yaml")); err != nil {
			fmt.Fprintf(os.Stderr, "\nWarning: Subchart %s not found at %s, skipping\n", dep.Name, subchartPath)
			continue
		}

		fmt.Printf("\n=== Converting subchart: %s ===\n", dep.Name)
		fmt.Printf("  Path: %s\n", subchartPath)

		conv, err := convertSubchartAndTrack(subchartPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
			continue
		}

		if len(conv.ConvertedPaths) == 0 {
			fmt.Println("  No conversions needed")
		} else {
			fmt.Printf("  Converted %d path(s):\n", len(conv.ConvertedPaths))
			for _, p := range conv.ConvertedPaths {
				fmt.Printf("    - %s (key=%s)\n", p.DotPath, p.MergeKey)
			}
			conversions = append(conversions, *conv)
		}
	}

	// Update umbrella values.yaml with converted subchart paths
	if len(conversions) > 0 {
		fmt.Printf("\n=== Updating umbrella values.yaml ===\n")
		if err := updateUmbrellaValues(umbrellaRoot, conversions); err != nil {
			fatal(err)
		}
	} else {
		fmt.Println("\nNo subcharts were converted, umbrella values.yaml unchanged.")
	}

	// Summary
	fmt.Println("\n=== Conversion Summary ===")
	totalPaths := 0
	for _, conv := range conversions {
		totalPaths += len(conv.ConvertedPaths)
	}
	fmt.Printf("Subcharts converted: %d\n", len(conversions))
	fmt.Printf("Total paths converted: %d\n", totalPaths)

	if !dryRun {
		fmt.Println("\nNote: Run 'helm dependency build' to rebuild chart dependencies.")
	}
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
		candidates, err := detectConversionCandidates(subchartPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
			continue
		}

		// Also check for user-defined rules
		userDetected := scanForUserRules(subchartPath)
		candidates = append(candidates, userDetected...)

		// Check template patterns
		var pathInfos []PathInfo
		for _, c := range candidates {
			pathInfos = append(pathInfos, PathInfo{
				DotPath:     c.ValuesPath,
				MergeKey:    c.MergeKey,
				SectionName: c.SectionName,
			})
		}
		matchedPaths := template.CheckTemplatePatterns(subchartPath, pathInfos)

		// Report results
		var detected, skipped []DetectedCandidate
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
		detected = checkCandidatesInValues(subchartPath, detected)

		// Separate by values existence
		var withValues, templateOnly []DetectedCandidate
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

// loadValuesNode loads values.yaml as a yaml.Node tree to preserve comments and formatting
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

func backupFile(path, ext string, original []byte) error {
	return os.WriteFile(path+ext, original, 0644)
}

// matchRule checks if a path matches any user-defined rule (for CRDs)
func matchRule(path []string) *Rule {
	dp := strings.Join(path, ".") + "[]"
	for _, r := range conf.Rules {
		if matchGlob(r.PathPattern, dp) {
			return &r
		}
	}
	return nil
}

func matchGlob(pattern, text string) bool {
	psegs := strings.Split(pattern, ".")
	tsegs := strings.Split(text, ".")
	i := len(psegs) - 1
	j := len(tsegs) - 1
	for i >= 0 && j >= 0 {
		if psegs[i] != "*" && psegs[i] != tsegs[j] {
			return false
		}
		i--
		j--
	}
	// Pattern fully consumed (i < 0) is a match
	if i < 0 {
		return true
	}
	// Pattern has remaining segments - only match if they're all wildcards
	for i >= 0 {
		if psegs[i] != "*" {
			return false
		}
		i--
	}
	return true
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
