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

type PathInfo struct {
	DotPath     string
	MergeKey    string // The patchMergeKey from K8s API (e.g., "name", "mountPath", "containerPort")
	SectionName string // The YAML section name (e.g., "volumes", "volumeMounts", "ports")
}

var (
	subcmd           string
	chartDir         string
	dryRun           bool
	backupExt        string
	configPath       string
	conf             Config
	transformedPaths []PathInfo
)

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

Use "helm list-to-map [command] --help" for more information about a command.
`)
}

func runDetect() {
	fs := flag.NewFlagSet("detect", flag.ExitOnError)
	fs.StringVar(&chartDir, "chart", ".", "path to chart root")
	fs.StringVar(&configPath, "config", "", "path to user config")
	verbose := false
	fs.BoolVar(&verbose, "v", false, "verbose output (show template files, partials, and warnings)")
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
  -v                    verbose output (show template files, partials, and warnings)

Examples:
  # Detect convertible fields in a chart
  helm list-to-map detect --chart ./my-chart

  # First load CRDs for Custom Resources, then detect
  helm list-to-map load-crd https://raw.githubusercontent.com/.../alertmanager-crd.yaml
  helm list-to-map detect --chart ./my-chart

  # Verbose output to see warnings and partial templates
  helm list-to-map detect --chart ./my-chart -v
`)
	}
	_ = fs.Parse(os.Args[2:])

	root, err := findChartRoot(chartDir)
	if err != nil {
		fatal(err)
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

	// Print detected candidates
	if len(allDetected) > 0 {
		fmt.Println("Detected convertible arrays:")
		for _, info := range allDetected {
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

	// Print warnings for undetected usages
	if len(result.Undetected) > 0 {
		fmt.Println()
		fmt.Println("Potentially convertible (not auto-detected):")
		for _, u := range result.Undetected {
			fmt.Printf("  %s (in %s:%d)\n", u.ValuesPath, u.TemplateFile, u.LineNumber)
			if verbose {
				fmt.Printf("    Reason: %s\n", u.Reason)
				fmt.Printf("    Suggestion: %s\n", u.Suggestion)
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

		fmt.Println()
		if verbose {
			fmt.Println("To manually add rules for undetected fields, use:")
			fmt.Println("  helm list-to-map add-rule --path='<path>[]' --uniqueKey=<key>")
			fmt.Println()
			fmt.Println("Run 'helm list-to-map add-rule --help' for more options.")
		} else {
			fmt.Println("Use -v for details. To add manual rules: helm list-to-map add-rule --help")
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

Examples:
  # Convert a chart with built-in K8s types
  helm list-to-map convert --chart ./my-chart

  # First load CRDs for Custom Resources, then convert
  helm list-to-map load-crd https://raw.githubusercontent.com/.../alertmanager-crd.yaml
  helm list-to-map convert --chart ./my-chart

  # Preview changes without modifying files
  helm list-to-map convert --dry-run
`)
	}
	_ = fs.Parse(os.Args[2:])

	root, err := findChartRoot(chartDir)
	if err != nil {
		fatal(err)
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

	// Build map of paths to convert for empty array handling
	candidateMap := make(map[string]DetectedCandidate)
	for _, c := range candidates {
		candidateMap[c.ValuesPath] = c
	}

	valuesPath := filepath.Join(root, "values.yaml")
	doc, raw, err := loadValuesNode(valuesPath)
	if err != nil {
		fatal(err)
	}

	// Use line-based editing to preserve original formatting
	var edits []ArrayEdit
	findArrayEdits(doc, nil, candidateMap, &edits)

	// Track all backup files created
	var backupFiles []string

	if len(edits) > 0 {
		out := applyLineEdits(raw, edits)

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
			walkForCount(doc, edit.Candidate.ValuesPath, &itemCount)

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
	} else {
		fmt.Println("No changes needed in values.yaml.")
	}

	var tchanges []string
	var helperCreated bool
	if !dryRun {
		var err error
		tchanges, backupFiles, err = rewriteTemplatesWithBackups(root, transformedPaths, backupExt, backupFiles)
		if err != nil {
			fatal(err)
		}

		if len(tchanges) > 0 {
			fmt.Println("\nUpdated templates:")
			for _, ch := range tchanges {
				fmt.Printf("  %s\n", ch)
			}
		}

		helperCreated = ensureHelpersWithReport(root)
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

	if len(edits) == 0 && len(tchanges) == 0 && !dryRun {
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

func runLoadCRD() {
	fs := flag.NewFlagSet("load-crd", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`
Load CRD (Custom Resource Definition) files to enable detection of convertible
fields in Custom Resources. CRDs are stored in the plugin's config directory
and automatically loaded when running 'detect' or 'convert'.

The plugin extracts x-kubernetes-list-type and x-kubernetes-list-map-keys
annotations from the CRD's OpenAPI schema to identify convertible list fields.

Usage:
  helm list-to-map load-crd <source> [source...]

Arguments:
  source    CRD file path, directory, or URL (can specify multiple)

Flags:
  -h, --help   help for load-crd

Examples:
  # Load CRD from a local file
  helm list-to-map load-crd ./alertmanager-crd.yaml

  # Load CRD from a URL
  helm list-to-map load-crd https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_alertmanagers.yaml

  # Load all CRDs from a directory (recursively)
  helm list-to-map load-crd ./my-chart/crds/

  # Load multiple CRDs using glob pattern
  helm list-to-map load-crd ./crds/crd-*.yaml
`)
	}
	_ = fs.Parse(os.Args[2:])

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Error: at least one CRD source is required")
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

	// Show what's now loaded
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	types := globalCRDRegistry.ListTypes()
	if len(types) > 0 {
		fmt.Printf("\nLoaded %d CRD type(s):\n", len(types))
		for _, t := range types {
			fields := globalCRDRegistry.fields[t]
			fmt.Printf("  %s (%d convertible fields)\n", t, len(fields))
		}
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

	// Extract filename from URL
	parts := strings.Split(url, "/")
	filename := parts[len(parts)-1]
	if filename == "" || !strings.HasSuffix(filename, ".yaml") {
		filename = "crd-" + fmt.Sprintf("%d", len(url)%10000) + ".yaml"
	}

	// Write to config directory
	destPath := filepath.Join(crdsDir, filename)
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

	filename := filepath.Base(source)

	// Write to config directory
	destPath := filepath.Join(crdsDir, filename)
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("writing to config: %w", err)
	}

	fmt.Printf("Loaded: %s -> %s\n", source, destPath)
	return nil
}

// loadAndStoreCRDsFromDirectory loads all CRD YAML files from a directory
func loadAndStoreCRDsFromDirectory(sourceDir, crdsDir string) error {
	var loaded int
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

		// Only try to load files that look like CRDs
		if !strings.Contains(strings.ToLower(filepath.Base(path)), "crd") {
			return nil
		}

		if err := loadAndStoreCRDFromFile(path, crdsDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s: %v\n", path, err)
			return nil
		}
		loaded++
		return nil
	})

	if err != nil {
		return err
	}

	if loaded == 0 {
		fmt.Fprintf(os.Stderr, "Warning: no CRD files found in %s\n", sourceDir)
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

	fmt.Printf("Loaded CRD types (%d):\n", len(types))
	for _, t := range types {
		fields := globalCRDRegistry.fields[t]
		fmt.Printf("\n%s\n", t)
		if verbose {
			for _, f := range fields {
				keys := strings.Join(f.MapKeys, ", ")
				fmt.Printf("  · %s (key: %s)\n", f.Path, keys)
			}
		} else {
			fmt.Printf("  %d convertible field(s)\n", len(fields))
		}
	}

	if !verbose && len(types) > 0 {
		fmt.Println("\nUse -v flag to see all convertible fields.")
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

func dotPath(path []string) string {
	return strings.Join(path, ".")
}

// getMaxLine returns the maximum line number within a yaml.Node tree
func getMaxLine(n *yaml.Node) int {
	max := n.Line
	for _, c := range n.Content {
		if c.Line > max {
			max = c.Line
		}
		if childMax := getMaxLine(c); childMax > max {
			max = childMax
		}
	}
	return max
}

// ArrayEdit represents a single array-to-map conversion with line info
type ArrayEdit struct {
	KeyLine        int    // Line number of the key (e.g., "volumes:")
	ValueStartLine int    // Line where the array value starts
	ValueEndLine   int    // Line where the array value ends
	KeyColumn      int    // Column of the key (for indentation)
	Replacement    string // The new map-format YAML
	Candidate      DetectedCandidate
}

// findArrayEdits walks the YAML tree and finds all arrays that need conversion
func findArrayEdits(node *yaml.Node, path []string, candidates map[string]DetectedCandidate, edits *[]ArrayEdit) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			findArrayEdits(child, path, candidates, edits)
		}

	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]

			key := keyNode.Value
			p := append(path, key)
			dp := dotPath(p)

			if candidate, isDetected := candidates[dp]; isDetected {
				if valueNode.Kind == yaml.SequenceNode {
					replacement := generateMapReplacement(valueNode, candidate, keyNode.Column)
					if replacement != "" {
						*edits = append(*edits, ArrayEdit{
							KeyLine:        keyNode.Line,
							ValueStartLine: valueNode.Line,
							ValueEndLine:   getMaxLine(valueNode),
							KeyColumn:      keyNode.Column,
							Replacement:    replacement,
							Candidate:      candidate,
						})
						continue
					}
				}
			}

			findArrayEdits(valueNode, p, candidates, edits)
		}

	case yaml.SequenceNode:
		for i, item := range node.Content {
			findArrayEdits(item, append(path, fmt.Sprintf("[%d]", i)), candidates, edits)
		}
	}
}

// generateMapReplacement generates the map-format YAML for an array
func generateMapReplacement(seqNode *yaml.Node, candidate DetectedCandidate, baseIndent int) string {
	mergeKey := candidate.MergeKey
	indent := strings.Repeat(" ", baseIndent)

	// Handle empty sequence: [] -> {}
	if len(seqNode.Content) == 0 {
		return "{}"
	}

	var lines []string
	for _, item := range seqNode.Content {
		if item.Kind != yaml.MappingNode {
			return "" // Can't convert non-mapping items
		}

		// Find the merge key value
		var keyValue string
		var keyIndex = -1
		for j := 0; j < len(item.Content); j += 2 {
			if item.Content[j].Value == mergeKey {
				keyValue = item.Content[j+1].Value
				keyIndex = j
				break
			}
		}

		if keyValue == "" {
			return "" // Merge key not found
		}

		// Start with the key
		lines = append(lines, fmt.Sprintf("%s%s:", indent, keyValue))

		// Add remaining fields
		for j := 0; j < len(item.Content); j += 2 {
			if j == keyIndex {
				continue // Skip the merge key
			}
			fieldKey := item.Content[j]
			fieldVal := item.Content[j+1]

			// Generate the field YAML
			fieldYAML := generateFieldYAML(fieldKey, fieldVal, baseIndent+2)
			lines = append(lines, fieldYAML)
		}
	}

	return strings.Join(lines, "\n")
}

// generateFieldYAML generates YAML for a single field with proper indentation
func generateFieldYAML(keyNode, valueNode *yaml.Node, indent int) string {
	indentStr := strings.Repeat(" ", indent)

	// Simple scalar value
	if valueNode.Kind == yaml.ScalarNode {
		val := valueNode.Value
		// Quote strings that need it
		if valueNode.Tag == "!!str" && needsQuoting(val) {
			val = fmt.Sprintf("%q", val)
		}
		return fmt.Sprintf("%s%s: %s", indentStr, keyNode.Value, val)
	}

	// Mapping value - needs nested output
	if valueNode.Kind == yaml.MappingNode {
		var lines []string
		lines = append(lines, fmt.Sprintf("%s%s:", indentStr, keyNode.Value))
		for j := 0; j < len(valueNode.Content); j += 2 {
			subKey := valueNode.Content[j]
			subVal := valueNode.Content[j+1]
			lines = append(lines, generateFieldYAML(subKey, subVal, indent+2))
		}
		return strings.Join(lines, "\n")
	}

	// Sequence value
	if valueNode.Kind == yaml.SequenceNode {
		var lines []string
		lines = append(lines, fmt.Sprintf("%s%s:", indentStr, keyNode.Value))
		for _, item := range valueNode.Content {
			if item.Kind == yaml.ScalarNode {
				lines = append(lines, fmt.Sprintf("%s  - %s", indentStr, item.Value))
			}
		}
		return strings.Join(lines, "\n")
	}

	return ""
}

// needsQuoting returns true if a string value needs to be quoted
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	// Check for special characters that need quoting
	for _, c := range s {
		if c == ':' || c == '#' || c == '[' || c == ']' || c == '{' || c == '}' || c == ',' || c == '&' || c == '*' || c == '!' || c == '|' || c == '>' || c == '\'' || c == '"' || c == '%' || c == '@' || c == '`' {
			return true
		}
	}
	return false
}

// walkForCount finds a sequence node by path and returns its item count
func walkForCount(node *yaml.Node, valuesPath string, count *int) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			walkForCount(child, valuesPath, count)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			if keyNode.Value == valuesPath {
				if valueNode.Kind == yaml.SequenceNode {
					*count = len(valueNode.Content)
				}
				return
			}
			walkForCount(valueNode, valuesPath, count)
		}
	}
}

// applyLineEdits applies line-based edits to the original file content
// This approach transforms array items in-place, preserving original formatting
func applyLineEdits(original []byte, edits []ArrayEdit) []byte {
	if len(edits) == 0 {
		return original
	}

	lines := strings.Split(string(original), "\n")

	// Sort edits by line number in reverse order (so we edit from bottom to top)
	// This way line numbers don't shift as we make edits
	sortedEdits := make([]ArrayEdit, len(edits))
	copy(sortedEdits, edits)
	for i := 0; i < len(sortedEdits)-1; i++ {
		for j := i + 1; j < len(sortedEdits); j++ {
			if sortedEdits[i].KeyLine < sortedEdits[j].KeyLine {
				sortedEdits[i], sortedEdits[j] = sortedEdits[j], sortedEdits[i]
			}
		}
	}

	for _, edit := range sortedEdits {
		keyLineIdx := edit.KeyLine - 1
		valueEndIdx := edit.ValueEndLine - 1

		if keyLineIdx < 0 || valueEndIdx >= len(lines) {
			continue
		}

		keyLine := lines[keyLineIdx]
		colonIdx := strings.Index(keyLine, ":")
		if colonIdx == -1 {
			continue
		}

		// Build the comment to insert (use key's column for proper indentation)
		// keyColumn is 1-based in yaml.Node, so subtract 1 for 0-based string index
		commentIndent := ""
		if edit.KeyColumn > 1 {
			commentIndent = strings.Repeat(" ", edit.KeyColumn-1)
		}
		// Build JSONPath-style comment: Kind.spec.path (key: mergeKey)
		jsonPath := edit.Candidate.YAMLPath
		if edit.Candidate.ResourceKind != "" {
			jsonPath = edit.Candidate.ResourceKind + "." + jsonPath
		}
		comment := fmt.Sprintf("%s# %s (key: %s)",
			commentIndent,
			jsonPath,
			edit.Candidate.MergeKey)

		afterColon := strings.TrimSpace(keyLine[colonIdx+1:])

		if afterColon == "[]" || afterColon == "{}" {
			// Inline empty array/map - add comment and change [] to {}
			// Also remove any commented-out array examples that follow
			newKeyLine := keyLine[:colonIdx+1] + " {}"

			// Find where commented-out examples end (lines starting with #, indented more than key)
			// These are stale array-syntax examples like "# - name: foo"
			endOfCommentedExamples := keyLineIdx + 1
			keyIndent := len(keyLine) - len(strings.TrimLeft(keyLine, " "))
			lastCommentLine := keyLineIdx // Track the last actual comment line

			for i := keyLineIdx + 1; i < len(lines); i++ {
				line := lines[i]
				trimmed := strings.TrimSpace(line)

				// Empty line - don't include it yet, wait to see if more comments follow
				if trimmed == "" {
					continue
				}

				// Check if this is a commented-out array item (indented comment)
				lineIndent := len(line) - len(strings.TrimLeft(line, " "))
				if strings.HasPrefix(trimmed, "#") && lineIndent > keyIndent {
					// This is an indented comment - likely a commented-out example
					lastCommentLine = i
					endOfCommentedExamples = i + 1
					continue
				}

				// Not a commented example, stop here
				break
			}

			// If we found commented examples, also skip any blank lines immediately after them
			// but preserve blank line separators before the next section
			if lastCommentLine > keyLineIdx {
				// Check if there's a blank line right after the last comment
				for i := lastCommentLine + 1; i < len(lines); i++ {
					if strings.TrimSpace(lines[i]) == "" {
						endOfCommentedExamples = i + 1
					} else {
						break
					}
				}
			}

			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:keyLineIdx]...)
			newLines = append(newLines, comment)
			newLines = append(newLines, newKeyLine)
			// Skip the commented-out examples, but add back a blank line if there was content removed
			if endOfCommentedExamples > keyLineIdx+1 && endOfCommentedExamples < len(lines) {
				// Add a blank line to preserve section separation
				newLines = append(newLines, "")
			}
			if endOfCommentedExamples < len(lines) {
				newLines = append(newLines, lines[endOfCommentedExamples:]...)
			}
			lines = newLines
		} else {
			// Multi-line array - transform each "- key: value" to "key:\n  otherfields"
			// Extract the array lines
			arrayLines := lines[keyLineIdx+1 : valueEndIdx+1]
			transformedLines := transformArrayToMap(arrayLines, edit.Candidate.MergeKey)

			// Build new content
			newLines := make([]string, 0, len(lines))
			newLines = append(newLines, lines[:keyLineIdx]...)
			newLines = append(newLines, comment)
			newLines = append(newLines, keyLine) // Keep original key line (e.g., "env:")
			newLines = append(newLines, transformedLines...)
			if valueEndIdx+1 < len(lines) {
				newLines = append(newLines, lines[valueEndIdx+1:]...)
			}
			lines = newLines
		}
	}

	return []byte(strings.Join(lines, "\n"))
}

// transformArrayToMap transforms YAML array lines to map format
// Input:  ["  - name: foo", "    value: bar", "  - name: baz", "    value: qux"]
// Output: ["  foo:", "    value: bar", "  baz:", "    value: qux"]
func transformArrayToMap(arrayLines []string, mergeKey string) []string {
	var result []string
	var currentItemLines []string
	var baseIndent string
	inItem := false

	for _, line := range arrayLines {
		trimmed := strings.TrimLeft(line, " ")

		// Check if this is a new array item (starts with "- ")
		if strings.HasPrefix(trimmed, "- ") {
			// Process previous item if any
			if inItem && len(currentItemLines) > 0 {
				transformed := transformSingleItem(currentItemLines, mergeKey, baseIndent)
				result = append(result, transformed...)
			}

			// Start new item
			currentItemLines = []string{line}
			baseIndent = strings.Repeat(" ", len(line)-len(trimmed))
			inItem = true
		} else if inItem {
			// Continuation of current item
			currentItemLines = append(currentItemLines, line)
		}
	}

	// Process last item
	if inItem && len(currentItemLines) > 0 {
		transformed := transformSingleItem(currentItemLines, mergeKey, baseIndent)
		result = append(result, transformed...)
	}

	return result
}

// transformSingleItem transforms a single array item from list to map format
func transformSingleItem(itemLines []string, mergeKey, baseIndent string) []string {
	if len(itemLines) == 0 {
		return nil
	}

	var result []string
	var mergeKeyValue string
	var mergeKeyLineComment string

	// Parse first line to extract merge key if present
	firstLine := itemLines[0]
	trimmed := strings.TrimLeft(firstLine, " ")
	if strings.HasPrefix(trimmed, "- ") {
		afterDash := strings.TrimPrefix(trimmed, "- ")

		// Check if merge key is on this line (e.g., "- name: foo")
		if strings.HasPrefix(afterDash, mergeKey+":") {
			// Extract the value after "name: "
			valueStart := len(mergeKey) + 2 // +2 for ": "
			rest := afterDash[valueStart:]

			// Handle line comments
			if commentIdx := strings.Index(rest, " #"); commentIdx >= 0 {
				mergeKeyValue = strings.TrimSpace(rest[:commentIdx])
				mergeKeyLineComment = rest[commentIdx:]
			} else {
				mergeKeyValue = strings.TrimSpace(rest)
			}

			// Start result with the map key
			result = append(result, fmt.Sprintf("%s%s:%s", baseIndent, mergeKeyValue, mergeKeyLineComment))

			// Add remaining fields from first line (if any after the merge key on same line)
			// This handles compact format like "- name: foo value: bar"
			// For now, assume standard format where other fields are on subsequent lines
		} else {
			// First line doesn't have merge key, look for it in subsequent lines
			// Meanwhile, add non-merge-key content from first line
			// Strip the "- " prefix and adjust indentation
			afterDash = strings.TrimSpace(afterDash)
			if afterDash != "" {
				parts := strings.SplitN(afterDash, ":", 2)
				if len(parts) == 2 {
					key := parts[0]
					val := strings.TrimSpace(parts[1])
					result = append(result, fmt.Sprintf("%s  %s: %s", baseIndent, key, val))
				}
			}
		}
	}

	// Process remaining lines
	for i := 1; i < len(itemLines); i++ {
		line := itemLines[i]
		trimmed := strings.TrimLeft(line, " ")

		// Check if this line contains the merge key
		if strings.HasPrefix(trimmed, mergeKey+":") && mergeKeyValue == "" {
			// Extract merge key value
			valueStart := len(mergeKey) + 2
			rest := trimmed[valueStart:]

			if commentIdx := strings.Index(rest, " #"); commentIdx >= 0 {
				mergeKeyValue = strings.TrimSpace(rest[:commentIdx])
				mergeKeyLineComment = rest[commentIdx:]
			} else {
				mergeKeyValue = strings.TrimSpace(rest)
			}

			// Insert the map key at the beginning
			keyLine := fmt.Sprintf("%s%s:%s", baseIndent, mergeKeyValue, mergeKeyLineComment)
			result = append([]string{keyLine}, result...)
		} else {
			// Regular field - keep it but adjust indentation
			// Original: 4 spaces + field (under "- name:")
			// New: 2 spaces + field (under "keyValue:")
			// The relative indentation stays the same
			result = append(result, line)
		}
	}

	return result
}

// matchRule checks if a path matches any user-defined rule (for CRDs)
func matchRule(path []string) *Rule {
	dp := dotPath(path) + "[]"
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

// rewriteTemplatesWithBackups rewrites templates and tracks backup files
func rewriteTemplatesWithBackups(chartPath string, paths []PathInfo, backupExtension string, existingBackups []string) ([]string, []string, error) {
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
			newContent = replaceListBlocks(newContent, p.DotPath, p.MergeKey, p.SectionName)
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

// replaceListBlocks replaces list rendering patterns with the generic helper
// Parameters:
//   - dotPath: the .Values path (e.g., "volumes")
//   - mergeKey: the patchMergeKey from K8s API (e.g., "name", "mountPath")
//   - sectionName: the YAML section name (e.g., "volumes", "volumeMounts")
func replaceListBlocks(tpl, dotPath, mergeKey, sectionName string) string {
	// Build the helper call that will replace matched patterns
	helperCall := fmt.Sprintf(`{{- include "chart.listmap.render" (dict "items" (index .Values %s) "key" %q "section" %q) }}`,
		quotePath(dotPath), mergeKey, sectionName)

	// Escape dotPath for regex (dots need escaping)
	escapedDotPath := regexp.QuoteMeta(dotPath)

	// Pattern 1: section:\n  {{- toYaml .Values.X | nindent N }}
	reA := regexp.MustCompile(`(?ms)` + regexp.QuoteMeta(sectionName) + `:\s*\n\s*\{\{\-?\s*toYaml\s+\.Values\.` + escapedDotPath + `\s*\|\s*nindent\s*\d+\s*\}\}`)
	tpl = reA.ReplaceAllString(tpl, helperCall)

	// Pattern 2: {{- with .Values.X }}\nsection:\n  {{- toYaml . | nindent N }}\n{{- end }}
	reB := regexp.MustCompile(`(?ms)\{\{-?\s*with\s+\.Values\.` + escapedDotPath + `\s*\}\}\s*` + regexp.QuoteMeta(sectionName) + `:\s*\n\s*\{\{\-?\s*toYaml\s+\.\s*\|\s*nindent\s*\d+\s*\}\}\s*\{\{-?\s*end\s*\}\}`)
	tpl = reB.ReplaceAllString(tpl, helperCall)

	// Pattern 3: section:\n  {{- range .Values.X }}...{{- end }}
	reC := regexp.MustCompile(`(?ms)` + regexp.QuoteMeta(sectionName) + `:\s*\n\s*\{\{\-?\s*range\s+.*?\.Values\.` + escapedDotPath + `\s*\}\}.*?\{\{\-?\s*end\s*\}\}`)
	tpl = reC.ReplaceAllString(tpl, helperCall)

	// Pattern 4: {{- if .Values.X }}\n  section:\n{{ toYaml .Values.X | indent N }}\n{{- end }}
	// This is a common pattern in many Helm charts (conditional rendering with indent)
	reD := regexp.MustCompile(`(?ms)\{\{-?\s*if\s+\.Values\.` + escapedDotPath + `\s*\}\}\s*\n\s*` + regexp.QuoteMeta(sectionName) + `:\s*\n\s*\{\{\s*toYaml\s+\.Values\.` + escapedDotPath + `\s*\|\s*indent\s*\d+\s*\}\}\s*\n\{\{-?\s*end\s*\}\}`)
	tpl = reD.ReplaceAllString(tpl, helperCall)

	// Pattern 5: {{- if .Values.X }}\n  section:\n  {{- toYaml .Values.X | nindent N }}\n{{- end }}
	// Same as pattern 4 but with nindent
	reE := regexp.MustCompile(`(?ms)\{\{-?\s*if\s+\.Values\.` + escapedDotPath + `\s*\}\}\s*\n\s*` + regexp.QuoteMeta(sectionName) + `:\s*\n\s*\{\{-?\s*toYaml\s+\.Values\.` + escapedDotPath + `\s*\|\s*nindent\s*\d+\s*\}\}\s*\n\{\{-?\s*end\s*\}\}`)
	tpl = reE.ReplaceAllString(tpl, helperCall)

	// Pattern 6: {{- include "chart.X.render" (dict "X" ...) }} - existing helper calls
	// This handles templates that were already converted with specialized helpers
	reF := regexp.MustCompile(`\{\{-?\s*include\s+"chart\.` + regexp.QuoteMeta(sectionName) + `\.render"\s*\(dict\s+"` + regexp.QuoteMeta(sectionName) + `"\s*\(index\s+\.Values\s+` + regexp.QuoteMeta(quotePath(dotPath)) + `\)\)\s*\}\}`)
	tpl = reF.ReplaceAllString(tpl, helperCall)

	return tpl
}

func quotePath(dotPath string) string {
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

// ensureHelpersWithReport creates helper template and returns true if created
func ensureHelpersWithReport(root string) bool {
	path := filepath.Join(root, "templates", "_listmap.tpl")
	if _, err := os.Stat(path); err == nil {
		return false // Already exists
	}
	err := os.WriteFile(path, []byte(strings.TrimSpace(listMapHelper())+"\n"), 0644)
	return err == nil
}

// listMapHelper returns a single generic helper template that works for any list type
// Parameters:
//   - items: the map of items (keyed by merge key value)
//   - key: the patchMergeKey field name (e.g., "name", "mountPath", "containerPort")
//   - section: the YAML section name (e.g., "volumes", "volumeMounts", "ports")
func listMapHelper() string {
	return `
{{- define "chart.listmap.render" -}}
{{- $items := .items -}}
{{- $key := .key -}}
{{- $section := .section -}}
{{- if $items }}
{{ $section }}:
{{- range $keyVal := keys $items | sortAlpha }}
{{- $spec := get $items $keyVal }}
  - {{ $key }}: {{ $keyVal | quote }}
{{- if $spec }}
{{ toYaml $spec | nindent 4 }}
{{- end }}
{{- end }}
{{- end }}
{{- end -}}`
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
