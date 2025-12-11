// Build to: bin/list-to-map
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Values = map[string]interface{}

// Rule represents a user-defined conversion rule for CRDs and custom resources
type Rule struct {
	PathPattern   string   `yaml:"pathPattern"`
	UniqueKeys    []string `yaml:"uniqueKeys"`
	Renderer      string   `yaml:"renderer"`
	PromoteScalar string   `yaml:"promoteScalar"`
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
	fs.Usage = func() {
		fmt.Print(`
Scan a Helm chart to detect arrays that can be converted to maps based on
unique key fields. This is a read-only operation that reports potential conversions
without modifying any files.

Usage:
  helm list-to-map detect [flags]

Flags:
      --chart string    path to chart root (default: current directory)
      --config string   path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
  -h, --help            help for detect
`)
	}
	_ = fs.Parse(os.Args[2:])

	root, err := findChartRoot(chartDir)
	if err != nil {
		fatal(err)
	}

	// Use new programmatic detection via K8s API introspection
	candidates, err := detectConversionCandidates(root)
	if err != nil {
		fatal(err)
	}

	// Also check for user-defined rules (for CRDs)
	userDetected := scanForUserRules(root)

	// Combine both sources
	allDetected := make(map[string]DetectedCandidate)
	for _, c := range candidates {
		allDetected[c.ValuesPath] = c
	}
	for _, c := range userDetected {
		if _, exists := allDetected[c.ValuesPath]; !exists {
			allDetected[c.ValuesPath] = c
		}
	}

	if len(allDetected) == 0 {
		fmt.Println("No convertible lists detected.")
		return
	}

	fmt.Println("Detected convertible arrays:")
	for _, info := range allDetected {
		typeInfo := ""
		if info.ElementType != "" {
			typeInfo = fmt.Sprintf(", type=%s", info.ElementType)
		}
		fmt.Printf("· %s → key=%s%s\n", info.ValuesPath, info.MergeKey, typeInfo)
	}
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
  1. Scans templates using K8s API introspection
  2. Identifies list fields with required unique keys
  3. Converts matching arrays to maps using unique key fields
  4. Updates template files to use new helper functions
  5. Generates helper templates if they don't exist

Usage:
  helm list-to-map convert [flags]

Flags:
      --backup-ext string   backup file extension (default: ".bak")
      --chart string        path to chart root (default: current directory)
      --config string       path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
      --dry-run             preview changes without writing files
  -h, --help                help for convert
`)
	}
	_ = fs.Parse(os.Args[2:])

	root, err := findChartRoot(chartDir)
	if err != nil {
		fatal(err)
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
	values, raw, err := loadValues(valuesPath)
	if err != nil {
		fatal(err)
	}
	var report strings.Builder
	changed := migrateValues(values, nil, &report, false, candidateMap)
	if changed {
		out, err := marshalYAML(values)
		if err != nil {
			fatal(err)
		}
		if dryRun {
			fmt.Println("=== values.yaml (updated preview) ===")
			fmt.Println(string(out))
		} else {
			if err := backupFile(valuesPath, backupExt, raw); err != nil {
				fatal(err)
			}
			if err := os.WriteFile(valuesPath, out, 0644); err != nil {
				fatal(err)
			}
		}
		fmt.Print(report.String())
	} else {
		fmt.Println("No changes needed in values.yaml.")
	}

	var tchanges []string
	if !dryRun {
		var err error
		tchanges, err = rewriteTemplatesNew(root, transformedPaths)
		if err != nil {
			fatal(err)
		}
		for _, ch := range tchanges {
			fmt.Printf("- Updated template: %s\n", ch)
		}

		ensureHelpers(root)
	} else if len(transformedPaths) > 0 {
		fmt.Println("Template changes would be applied (use without --dry-run to apply):")
		for _, p := range transformedPaths {
			fmt.Printf("- Would update templates using %s\n", p.DotPath)
		}
	}

	if !changed && len(tchanges) == 0 && !dryRun {
		fmt.Println("Nothing to convert.")
	}
}

func runAddRule() {
	path := ""
	uniqueKey := ""
	renderer := "generic"
	fs := flag.NewFlagSet("add-rule", flag.ExitOnError)
	fs.StringVar(&path, "path", "", "dot path to array (end with []), e.g. database.primary.extraEnv[]")
	fs.StringVar(&uniqueKey, "uniqueKey", "", "unique key field, e.g. name")
	fs.StringVar(&renderer, "renderer", "generic", "renderer hint: env|volumeMounts|volumes|ports|generic")
	fs.StringVar(&configPath, "config", "", "path to user config")
	fs.Usage = func() {
		fmt.Print(`
Add a custom conversion rule to your user configuration file. Use this when you need
to convert arrays in custom CRDs or non-standard paths that aren't covered by built-in rules.

Usage:
  helm list-to-map add-rule [flags]

Flags:
      --config string      path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
  -h, --help               help for add-rule
      --path string        dot path to array (end with []), e.g. database.primary.extraEnv[]
      --renderer string    renderer hint: env|volumeMounts|volumes|ports|generic (default: "generic")
      --uniqueKey string   unique key field, e.g. name

Examples:
  helm list-to-map add-rule --path='istio.virtualService.http[]' --uniqueKey=name
  helm list-to-map add-rule --path='myapp.listeners[]' --uniqueKey=port --renderer=generic
`)
	}
	_ = fs.Parse(os.Args[2:])

	if path == "" || uniqueKey == "" {
		fmt.Fprintln(os.Stderr, "Error: --path and --uniqueKey are required")
		fmt.Fprintln(os.Stderr, "Run 'helm list-to-map add-rule --help' for usage.")
		os.Exit(1)
	}

	r := Rule{PathPattern: path, UniqueKeys: []string{uniqueKey}, Renderer: renderer}
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
	fmt.Printf("Added rule to %s: %s (key=%s, renderer=%s)\n", user, path, uniqueKey, renderer)
}

func runListRules() {
	// Check for help flag
	for _, arg := range os.Args[2:] {
		if arg == "-h" || arg == "--help" {
			fmt.Print(`
List all active conversion rules, including both built-in rules for common
Kubernetes patterns and any custom rules you've added.

Usage:
  helm list-to-map rules [flags]

Flags:
  -h, --help   help for rules
`)
			return
		}
	}

	all := conf.Rules
	fmt.Println("Rules (built-in + user):")
	for _, r := range all {
		fmt.Printf("- %s (keys=%v, renderer=%s)\n", r.PathPattern, r.UniqueKeys, r.Renderer)
	}
}

func defaultUserConfigPath() string {
	home := os.Getenv("HELM_CONFIG_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".config", "helm")
	}
	return filepath.Join(home, "list-to-map", "config.yaml")
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
	return "", fmt.Errorf("Chart.yaml not found starting from %s", start)
}

func loadValues(path string) (Values, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var v Values
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, nil, err
	}
	return v, data, nil
}

func marshalYAML(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

func backupFile(path, ext string, original []byte) error {
	return os.WriteFile(path+ext, original, 0644)
}

func dotPath(path []string) string {
	return strings.Join(path, ".")
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

func copyWithoutKey(m map[string]interface{}, key string) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if k == key {
			continue
		}
		out[k] = v
	}
	return out
}

// migrateValues migrates arrays to maps based on detected candidates from K8s API introspection
func migrateValues(node interface{}, path []string, report *strings.Builder, detectOnly bool, candidates map[string]DetectedCandidate) bool {
	changed := false
	switch t := node.(type) {
	case map[string]interface{}:
		for k, v := range t {
			p := append(path, k)
			dp := dotPath(p)

			// Check if this path should be converted based on detection
			if candidate, isDetected := candidates[dp]; isDetected {
				// Handle empty array case: [] -> {}
				if arr, ok := v.([]interface{}); ok && len(arr) == 0 {
					if detectOnly {
						continue
					}
					// Convert empty array to empty map
					t[k] = map[string]interface{}{}
					changed = true
					report.WriteString(fmt.Sprintf("- Migrated %s (key=%s) [empty array]\n", candidate.ValuesPath, candidate.MergeKey))
					transformedPaths = append(transformedPaths, PathInfo{
						DotPath:     candidate.ValuesPath,
						MergeKey:    candidate.MergeKey,
						SectionName: candidate.SectionName,
					})
					continue
				}
				// Handle populated array
				if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
					out, did := convertArrayWithCandidate(arr, candidate)
					if did {
						if detectOnly {
							report.WriteString(fmt.Sprintf("· %s → key=%s\n", candidate.ValuesPath, candidate.MergeKey))
						} else {
							t[k] = out
							changed = true
							report.WriteString(fmt.Sprintf("- Migrated %s (key=%s)\n", candidate.ValuesPath, candidate.MergeKey))
							transformedPaths = append(transformedPaths, PathInfo{
								DotPath:     candidate.ValuesPath,
								MergeKey:    candidate.MergeKey,
								SectionName: candidate.SectionName,
							})
						}
						continue
					}
				}
			}

			if v == nil {
				continue
			}
			// Recurse into nested structures
			if _, ok := v.([]interface{}); ok {
				if migrateValues(v, p, report, detectOnly, candidates) {
					changed = true
				}
				continue
			}
			if migrateValues(v, p, report, detectOnly, candidates) {
				changed = true
			}
		}
	case []interface{}:
		for i := range t {
			if migrateValues(t[i], append(path, fmt.Sprintf("[%d]", i)), report, detectOnly, candidates) {
				changed = true
			}
		}
	}
	return changed
}

// convertArrayWithCandidate converts an array to a map using the candidate's merge key
func convertArrayWithCandidate(arr []interface{}, candidate DetectedCandidate) (map[string]interface{}, bool) {
	out := map[string]interface{}{}
	key := candidate.MergeKey

	for _, it := range arr {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		nameAny, has := m[key]
		nameStr, _ := nameAny.(string)
		if !has || strings.TrimSpace(nameStr) == "" {
			continue
		}
		entry := copyWithoutKey(m, key)
		out[nameStr] = entry
	}

	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// rewriteTemplatesNew rewrites templates using the new PathInfo structure
// Uses a single generic helper that takes key and section as parameters
func rewriteTemplatesNew(chartPath string, paths []PathInfo) ([]string, error) {
	var changed []string
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
			if err := backupFile(path, backupExt, data); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
				return err
			}
			changed = append(changed, rel(chartPath, path))
		}
		return nil
	})
	return changed, err
}

// replaceListBlocks replaces list rendering patterns with the generic helper
// Parameters:
//   - dotPath: the .Values path (e.g., "volumes")
//   - mergeKey: the patchMergeKey from K8s API (e.g., "name", "mountPath")
//   - sectionName: the YAML section name (e.g., "volumes", "volumeMounts")
func replaceListBlocks(tpl, dotPath, mergeKey, sectionName string) string {
	helperCall := fmt.Sprintf(`{{- include "chart.listmap.render" (dict "items" (index .Values %s) "key" %q "section" %q) }}`,
		quotePath(dotPath), mergeKey, sectionName)

	// Pattern 1: section:\n  {{- toYaml .Values.X | nindent N }}
	reA := regexp.MustCompile(`(?ms)` + regexp.QuoteMeta(sectionName) + `:\s*\n\s*\{\{\-?\s*toYaml\s+\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*\|\s*nindent\s*\d+\s*\}\}`)
	tpl = reA.ReplaceAllString(tpl, helperCall)

	// Pattern 2: {{- with .Values.X }}\nsection:\n  {{- toYaml . | nindent N }}\n{{- end }}
	reB := regexp.MustCompile(`(?ms)\{\{-?\s*with\s+\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*\}\}\s*` + regexp.QuoteMeta(sectionName) + `:\s*\n\s*\{\{\-?\s*toYaml\s+\.\s*\|\s*nindent\s*\d+\s*\}\}\s*\{\{-?\s*end\s*\}\}`)
	tpl = reB.ReplaceAllString(tpl, helperCall)

	// Pattern 3: section:\n  {{- range .Values.X }}...{{- end }}
	reC := regexp.MustCompile(`(?ms)` + regexp.QuoteMeta(sectionName) + `:\s*\n\s*\{\{\-?\s*range\s+.*?\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*\}\}.*?\{\{\-?\s*end\s*\}\}`)
	tpl = reC.ReplaceAllString(tpl, helperCall)

	// Pattern 4: {{- include "chart.X.render" (dict "X" ...) }} - existing helper calls
	// This handles templates that were already converted with specialized helpers
	reD := regexp.MustCompile(`\{\{-?\s*include\s+"chart\.` + regexp.QuoteMeta(sectionName) + `\.render"\s*\(dict\s+"` + regexp.QuoteMeta(sectionName) + `"\s*\(index\s+\.Values\s+` + regexp.QuoteMeta(quotePath(dotPath)) + `\)\)\s*\}\}`)
	tpl = reD.ReplaceAllString(tpl, helperCall)

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

func ensureHelpers(root string) {
	path := filepath.Join(root, "templates", "_listmap.tpl")
	if _, err := os.Stat(path); err == nil {
		return
	}
	_ = os.WriteFile(path, []byte(strings.TrimSpace(listMapHelper())+"\n"), 0644)
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
