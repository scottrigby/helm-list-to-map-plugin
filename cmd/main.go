// Build to: bin/list-to-map
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
)

type Values = map[string]interface{}

type Rule struct {
	PathPattern   string   `yaml:"pathPattern"`
	UniqueKeys    []string `yaml:"uniqueKeys"`
	Renderer      string   `yaml:"renderer"`
	PromoteScalar string   `yaml:"promoteScalar"`
}

type Config struct {
	Rules              []Rule `yaml:"rules"`
	LastWinsDuplicates bool   `yaml:"lastWinsDuplicates"`
	SortKeys           bool   `yaml:"sortKeys"`
}

type PathInfo struct {
	DotPath   string
	Renderer  string
	UniqueKey string
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

// K8s API type mapping for built-in renderers
func getAPIType(renderer string) reflect.Type {
	switch renderer {
	case "env":
		return reflect.TypeOf(corev1.EnvVar{})
	case "volumeMounts":
		return reflect.TypeOf(corev1.VolumeMount{})
	case "volumes":
		return reflect.TypeOf(corev1.Volume{})
	case "ports":
		return reflect.TypeOf(corev1.ContainerPort{})
	case "containers":
		return reflect.TypeOf(corev1.Container{})
	default:
		return nil
	}
}

// isRequiredField checks if a field is required (not optional) in the K8s API.
// A field is considered required if:
// - It exists in the API type
// - It doesn't have "omitempty" in its json tag
// - It's not a pointer type (pointers indicate optional fields)
func isRequiredField(apiType reflect.Type, fieldName string) bool {
	if apiType == nil {
		return false
	}

	// Convert field name to Go struct field name (capitalize first letter)
	goFieldName := strings.ToUpper(fieldName[:1]) + fieldName[1:]

	field, found := apiType.FieldByName(goFieldName)
	if !found {
		return false
	}

	jsonTag := field.Tag.Get("json")
	// Field is required if it doesn't have omitempty AND is not a pointer
	hasOmitempty := strings.Contains(jsonTag, "omitempty")
	isPointer := field.Type.Kind() == reflect.Ptr

	return !hasOmitempty && !isPointer
}

// validateUniqueKey checks if the unique key is valid for the given renderer.
// For built-in K8s types, it validates against the API schema.
// For generic/custom types, it always returns true.
func validateUniqueKey(renderer string, uniqueKey string) bool {
	apiType := getAPIType(renderer)
	if apiType == nil {
		// No API type available (generic renderer or custom CRD)
		// Allow conversion - user knows their schema
		return true
	}

	// For K8s types, require the unique key to be a required field
	return isRequiredField(apiType, uniqueKey)
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

	// Load config for all commands (needed for rules display)
	conf = defaultConfig()
	if configPath == "" {
		configPath = defaultUserConfigPath()
	}
	if b, err := os.ReadFile(configPath); err == nil {
		var uc Config
		if yaml.Unmarshal(b, &uc) == nil {
			mergeConfig(&conf, &uc)
		}
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

	// Phase 1: Template-first detection - scan templates for list usage
	templateDetected, err := scanTemplatesForListUsage(root)
	if err != nil {
		fatal(err)
	}

	// Phase 2: Content-based detection - scan values.yaml for populated arrays
	valuesPath := filepath.Join(root, "values.yaml")
	values, _, err := loadValues(valuesPath)
	if err != nil {
		fatal(err)
	}
	var report strings.Builder
	_ = migrateRecursive(values, nil, &report, true, nil)

	// Combine both detection sources
	allDetected := make(map[string]PathInfo)

	// Add template-detected paths
	for _, info := range templateDetected {
		allDetected[info.DotPath] = info
	}

	// Add content-detected paths (from migrateRecursive via transformedPaths)
	for _, info := range transformedPaths {
		allDetected[info.DotPath] = info
	}

	// Clear transformedPaths for next run
	transformedPaths = nil

	if len(allDetected) == 0 {
		fmt.Println("No convertible lists detected.")
		return
	}

	fmt.Println("Detected convertible arrays:")
	for _, info := range allDetected {
		source := "(found in templates)"
		// Check if also found in values
		if report.Len() > 0 && strings.Contains(report.String(), info.DotPath) {
			source = "(found in templates and values)"
		}
		fmt.Printf("· %s → key=%s, renderer=%s %s\n", info.DotPath, info.UniqueKey, info.Renderer, source)
	}
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
  1. Scans templates for list usage patterns
  2. Scans values.yaml for arrays matching configured patterns
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

	// Phase 1: Template-first detection
	templateDetected, err := scanTemplatesForListUsage(root)
	if err != nil {
		fatal(err)
	}

	// Build map of paths to convert for empty array handling
	templatePaths := make(map[string]PathInfo)
	for _, info := range templateDetected {
		templatePaths[info.DotPath] = info
	}

	valuesPath := filepath.Join(root, "values.yaml")
	values, raw, err := loadValues(valuesPath)
	if err != nil {
		fatal(err)
	}
	var report strings.Builder
	changed := migrateRecursive(values, nil, &report, false, templatePaths)
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

	tchanges, err := rewriteTemplates(root, transformedPaths)
	if err != nil {
		fatal(err)
	}
	for _, ch := range tchanges {
		fmt.Printf("- Updated template: %s\n", ch)
	}

	ensureHelpers(root)

	if !changed && len(tchanges) == 0 {
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

func defaultConfig() Config {
	return Config{
		Rules: []Rule{
			{PathPattern: "*.env[]", UniqueKeys: []string{"name"}, Renderer: "env", PromoteScalar: "env:value"},
			{PathPattern: "*.*.extraEnv[]", UniqueKeys: []string{"name"}, Renderer: "env"},
			{PathPattern: "*.volumeMounts[]", UniqueKeys: []string{"name", "mountPath"}, Renderer: "volumeMounts"},
			{PathPattern: "*.volumes[]", UniqueKeys: []string{"name"}, Renderer: "volumes"},
			{PathPattern: "*.ports[]", UniqueKeys: []string{"name", "containerPort", "port"}, Renderer: "ports"},
			{PathPattern: "*.imagePullSecrets[]", UniqueKeys: []string{"name"}, Renderer: "generic"},
			{PathPattern: "*.httpGet.headers[]", UniqueKeys: []string{"name"}, Renderer: "generic"},
			{PathPattern: "*.containers[]", UniqueKeys: []string{"name"}, Renderer: "containers"},
		},
		LastWinsDuplicates: true,
		SortKeys:           true,
	}
}

func mergeConfig(base, add *Config) {
	base.Rules = append(base.Rules, add.Rules...)
	if add.LastWinsDuplicates {
		base.LastWinsDuplicates = true
	}
	if add.SortKeys {
		base.SortKeys = true
	}
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

func migrateRecursive(node interface{}, path []string, report *strings.Builder, detectOnly bool, templatePaths map[string]PathInfo) bool {
	changed := false
	switch t := node.(type) {
	case map[string]interface{}:
		for k, v := range t {
			p := append(path, k)
			dp := dotPath(p)

			// Check if this path should be converted based on template detection
			if templateInfo, isTemplateDetected := templatePaths[dp]; isTemplateDetected {
				// Handle empty array case: [] -> {}
				if arr, ok := v.([]interface{}); ok && len(arr) == 0 {
					if detectOnly {
						// Don't report empty arrays in detect mode - they're handled by template scan
						continue
					}
					// Convert empty array to empty map
					t[k] = map[string]interface{}{}
					changed = true
					report.WriteString(fmt.Sprintf("- Migrated %s (key=%s, renderer=%s) [empty array]\n", templateInfo.DotPath, templateInfo.UniqueKey, templateInfo.Renderer))
					transformedPaths = append(transformedPaths, templateInfo)
					continue
				}
			}

			if v == nil {
				continue
			}
			if arr, ok := v.([]interface{}); ok {
				if out, did, info := convertArrayAtPath(arr, p, detectOnly); did {
					if detectOnly {
						report.WriteString(fmt.Sprintf("· %s → key=%s, renderer=%s\n", info.DotPath, info.UniqueKey, info.Renderer))
					} else {
						t[k] = out
						changed = true
						report.WriteString(fmt.Sprintf("- Migrated %s (key=%s, renderer=%s)\n", info.DotPath, info.UniqueKey, info.Renderer))
						transformedPaths = append(transformedPaths, info)
					}
				} else {
					for i := range arr {
						if migrateRecursive(arr[i], append(p, fmt.Sprintf("[%d]", i)), report, detectOnly, templatePaths) {
							changed = true
						}
					}
				}
				continue
			}
			if migrateRecursive(v, p, report, detectOnly, templatePaths) {
				changed = true
			}
		}
	case []interface{}:
		for i := range t {
			if migrateRecursive(t[i], append(path, fmt.Sprintf("[%d]", i)), report, detectOnly, templatePaths) {
				changed = true
			}
		}
	}
	return changed
}

func dotPath(path []string) string {
	return strings.Join(path, ".")
}

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

func convertArrayAtPath(arr []interface{}, path []string, detectOnly bool) (interface{}, bool, PathInfo) {
	rule := matchRule(path)
	if rule == nil {
		return arr, false, PathInfo{}
	}
	key := pickKey(arr, rule.UniqueKeys)
	if key == "" {
		return arr, false, PathInfo{}
	}

	// Validate that the unique key is required (not optional) in K8s API
	if !validateUniqueKey(rule.Renderer, key) {
		// Skip conversion - unique key is optional
		return arr, false, PathInfo{}
	}

	if detectOnly {
		return nil, true, PathInfo{DotPath: dotPath(path), Renderer: rule.Renderer, UniqueKey: key}
	}

	out := map[string]interface{}{}
	dups := map[string]int{}
	missing := 0

	for _, it := range arr {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		nameAny, has := m[key]
		nameStr, _ := nameAny.(string)
		if !has || strings.TrimSpace(nameStr) == "" {
			missing++
			continue
		}
		entry := copyWithoutKey(m, key)
		if _, exists := out[nameStr]; exists {
			dups[nameStr]++
		}
		out[nameStr] = entry
	}

	_ = dups
	_ = missing
	info := PathInfo{DotPath: dotPath(path), Renderer: rule.Renderer, UniqueKey: key}
	return out, true, info
}

func pickKey(arr []interface{}, candidates []string) string {
	counts := make(map[string]int)
	for _, it := range arr {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		for _, k := range candidates {
			if v, ok := m[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					counts[k]++
				}
			}
		}
	}
	best := ""
	bestCount := 0
	for _, k := range candidates {
		if counts[k] > bestCount {
			best = k
			bestCount = counts[k]
		}
	}
	if bestCount*2 >= len(arr) && best != "" {
		return best
	}
	return ""
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

// scanTemplatesForListUsage scans templates directory for list-rendering patterns
// and returns PathInfo for arrays that match configured rules, regardless of
// whether the arrays are empty or populated in values.yaml
func scanTemplatesForListUsage(chartRoot string) ([]PathInfo, error) {
	var detected []PathInfo
	seen := make(map[string]bool) // dedup by dotPath
	tdir := filepath.Join(chartRoot, "templates")

	// Regex patterns for detecting list-rendering in templates:
	// 1. toYaml pattern: {{- toYaml .Values.path | nindent N }}
	reToYaml := regexp.MustCompile(`\{\{-?\s*toYaml\s+\.Values\.([a-zA-Z0-9_.]+)\s*\|`)
	// 2. with + toYaml pattern: {{- with .Values.path }} ... {{- toYaml . | ... }}
	reWith := regexp.MustCompile(`\{\{-?\s*with\s+\.Values\.([a-zA-Z0-9_.]+)\s*\}\}`)
	// 3. range pattern: {{- range .Values.path }}
	reRange := regexp.MustCompile(`\{\{-?\s*range\s+.*?\.Values\.([a-zA-Z0-9_.]+)\s*\}\}`)

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

		// Check each extracted path against configured rules
		for pathStr := range paths {
			if seen[pathStr] {
				continue
			}

			// Convert dot path to path segments for rule matching
			// matchRule expects segments and will add [] internally
			segments := strings.Split(pathStr, ".")
			rule := matchRule(segments)
			if rule == nil {
				continue
			}

			// Determine unique key - prefer 'name' if available, otherwise use first key
			uniqueKey := rule.UniqueKeys[0]
			for _, k := range rule.UniqueKeys {
				if k == "name" {
					uniqueKey = k
					break
				}
			}

			// Validate that the unique key is required (not optional) in K8s API
			if !validateUniqueKey(rule.Renderer, uniqueKey) {
				// Skip this path - unique key is optional
				continue
			}

			// Found a valid match - this path should be converted
			seen[pathStr] = true
			detected = append(detected, PathInfo{
				DotPath:   pathStr,
				Renderer:  rule.Renderer,
				UniqueKey: uniqueKey,
			})
		}

		return nil
	})

	return detected, err
}

func rewriteTemplates(chartPath string, paths []PathInfo) ([]string, error) {
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
		new := orig

		for _, p := range paths {
			switch p.Renderer {
			case "env":
				new = replaceEnvBlocksForPath(new, p.DotPath)
			case "volumeMounts":
				new = replaceBlocksForPath(new, p.DotPath, "chart.volumeMounts.render", "volumeMounts")
			case "volumes":
				new = replaceBlocksForPath(new, p.DotPath, "chart.volumes.render", "volumes")
			case "ports":
				new = replaceBlocksForPath(new, p.DotPath, "chart.ports.render", "ports")
			case "generic":
				new = replaceBlocksForPath(new, p.DotPath, "chart.listmap.render", "items")
			}
		}

		if new != orig {
			if err := backupFile(path, backupExt, data); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(new), 0644); err != nil {
				return err
			}
			changed = append(changed, rel(chartPath, path))
		}
		return nil
	})
	return changed, err
}

func replaceEnvBlocksForPath(tpl, dotPath string) string {
	reA := regexp.MustCompile(`(?ms)env:\s*\n\s*\{\{\-?\s*toYaml\s+\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*\|\s*nindent\s*\d+\s*\}\}`)
	tpl = reA.ReplaceAllString(tpl, `{{- include "chart.env.render" (dict "env" (index .Values `+quotePath(dotPath)+`)) }}`)
	reB := regexp.MustCompile(`(?ms){{-?\s*with\s+\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*}}\s*env:\s*\n\s*\{\{\-?\s*toYaml\s+\.\s*\|\s*nindent\s*\d+\s*\}\}\s*{{-?\s*end\s*}}`)
	tpl = reB.ReplaceAllString(tpl, `{{- include "chart.env.render" (dict "env" (index .Values `+quotePath(dotPath)+`)) }}`)
	reC := regexp.MustCompile(`(?ms)env:\s*\n\s*\{\{\-?\s*range\s+.*?\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*\}\}.*?\{\{\-?\s*end\s*\}\}`)
	tpl = reC.ReplaceAllString(tpl, `{{- include "chart.env.render" (dict "env" (index .Values `+quotePath(dotPath)+`)) }}`)
	return tpl
}

func replaceBlocksForPath(tpl, dotPath, includeName, section string) string {
	reA := regexp.MustCompile(`(?ms)` + section + `:\s*\n\s*\{\{\-?\s*toYaml\s+\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*\|\s*nindent\s*\d+\s*\}\}`)
	tpl = reA.ReplaceAllString(tpl, `{{- include "`+includeName+`" (dict "`+section+`" (index .Values `+quotePath(dotPath)+`)) }}`)
	reB := regexp.MustCompile(`(?ms){{-?\s*with\s+\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*}}\s*` + section + `:\s*\n\s*\{\{\-?\s*toYaml\s+\.\s*\|\s*nindent\s*\d+\s*\}\}\s*{{-?\s*end\s*}}`)
	tpl = reB.ReplaceAllString(tpl, `{{- include "`+includeName+`" (dict "`+section+`" (index .Values `+quotePath(dotPath)+`)) }}`)
	reC := regexp.MustCompile(`(?ms)` + section + `:\s*\n\s*\{\{\-?\s*range\s+.*?\.Values\.` + regexp.QuoteMeta(dotPath) + `\s*\}\}.*?\{\{\-?\s*end\s*\}\}`)
	tpl = reC.ReplaceAllString(tpl, `{{- include "`+includeName+`" (dict "`+section+`" (index .Values `+quotePath(dotPath)+`)) }}`)
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
	write := func(name, content string) {
		path := filepath.Join(root, "templates", name)
		if _, err := os.Stat(path); err == nil {
			return
		}
		_ = os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0644)
	}
	write("_env.tpl", envHelper())
	write("_volumeMounts.tpl", volumeMountsHelper())
	write("_volumes.tpl", volumesHelper())
	write("_ports.tpl", portsHelper())
	write("_listmap.tpl", listMapHelper())
}

func envHelper() string {
	return `
{{- define "chart.env.render" -}}
{{- $env := .env -}}
{{- if $env }}
env:
{{- range $name := keys $env | sortAlpha }}
{{- $spec := get $env $name }}
  - name: {{ $name | quote }}
{{ toYaml $spec | nindent 4 }}
{{- end }}
{{- end }}
{{- end -}}`
}

func volumeMountsHelper() string {
	return `
{{- define "chart.volumeMounts.render" -}}
{{- $vm := .volumeMounts -}}
{{- if $vm }}
volumeMounts:
{{- range $name := keys $vm | sortAlpha }}
{{- $spec := get $vm $name }}
  - name: {{ $name | quote }}
{{ toYaml $spec | nindent 4 }}
{{- end }}
{{- end }}
{{- end -}}`
}

func volumesHelper() string {
	return `
{{- define "chart.volumes.render" -}}
{{- $vol := .volumes -}}
{{- if $vol }}
volumes:
{{- range $name := keys $vol | sortAlpha }}
{{- $spec := get $vol $name }}
  - name: {{ $name | quote }}
{{ toYaml $spec | nindent 4 }}
{{- end }}
{{- end }}
{{- end -}}`
}

func portsHelper() string {
	return `
{{- define "chart.ports.render" -}}
{{- $ports := .ports -}}
{{- if $ports }}
ports:
{{- range $name := keys $ports | sortAlpha }}
{{- $spec := get $ports $name }}
  - name: {{ $name | default "" | quote }}
{{ toYaml $spec | nindent 4 }}
{{- end }}
{{- end }}
{{- end -}}`
}

func listMapHelper() string {
	return `
{{- define "chart.listmap.render" -}}
{{- $items := .items -}}
{{- if $items }}
items:
{{- range $name := keys $items | sortAlpha }}
{{- $spec := get $items $name }}
  - name: {{ $name | quote }}
{{ toYaml $spec | nindent 4 }}
{{- end }}
{{- end }}
{{- end -}}`
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
