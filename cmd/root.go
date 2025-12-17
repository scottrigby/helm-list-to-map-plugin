// Build to: bin/list-to-map
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/template"
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

// SubchartConversion tracks what was converted in a subchart
type SubchartConversion struct {
	Name           string              // Subchart name (used as prefix in umbrella values)
	ConvertedPaths []template.PathInfo // Paths that were converted
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
	Annotations  map[string]string `yaml:"annotations,omitempty"`
	Sources      []string          `yaml:"sources,omitempty"`
}

// Global config loaded from user config file
var conf Config

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	subcmd := os.Args[1]

	// Handle top-level help
	if subcmd == "-h" || subcmd == "--help" {
		usage()
		return
	}

	// Load user-defined rules for CRDs and custom resources
	configPath := os.Getenv("HELM_LIST_TO_MAP_CONFIG")
	if configPath == "" {
		configPath = defaultUserConfigPath()
	}
	if b, err := os.ReadFile(configPath); err == nil {
		_ = yaml.Unmarshal(b, &conf)
	}

	var err error
	switch subcmd {
	case "detect":
		err = runDetectCommand()
	case "convert":
		err = runConvertCommand()
	case "add-rule":
		err = runAddRuleCommand()
	case "rules":
		err = runListRulesCommand()
	case "load-crd":
		err = runLoadCRDCommand()
	case "list-crds":
		err = runListCRDsCommand()
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q for \"helm list-to-map\"\n", subcmd)
		fmt.Fprintf(os.Stderr, "Run 'helm list-to-map --help' for usage.\n")
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
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

// Command wrapper functions that parse flags and create Options structs

func runDetectCommand() error {
	fs := flag.NewFlagSet("detect", flag.ExitOnError)
	opts := DetectOptions{}
	fs.StringVar(&opts.ChartDir, "chart", ".", "path to chart root")
	fs.StringVar(&opts.ConfigPath, "config", "", "path to user config")
	fs.BoolVar(&opts.Verbose, "v", false, "verbose output")
	fs.BoolVar(&opts.Recursive, "recursive", false, "recursively detect in file:// subcharts")
	fs.BoolVar(&opts.IncludeChartsDir, "include-charts-dir", false, "include subcharts in charts/ directory")
	fs.BoolVar(&opts.ExpandRemote, "expand-remote", false, "expand and process .tgz files in charts/")
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
      --chart string         path to chart root (default: current directory)
      --config string        path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
      --expand-remote        expand and process .tgz files in charts/
  -h, --help                 help for detect
      --include-charts-dir   include subcharts in charts/ directory
      --recursive            recursively detect in file:// subcharts (for umbrella charts)
  -v                         verbose output (show template files, partials, and warnings)

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

  # Detect in umbrella chart including embedded charts/ subcharts
  helm list-to-map detect --chart ./umbrella-chart --include-charts-dir

  # Process all dependency types (file://, charts/ dirs, .tgz files)
  helm list-to-map detect --chart ./umbrella-chart --recursive --include-charts-dir --expand-remote
`)
	}
	_ = fs.Parse(os.Args[2:])
	return runDetect(opts)
}

func runConvertCommand() error {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	opts := ConvertOptions{}
	fs.StringVar(&opts.ChartDir, "chart", ".", "path to chart root")
	fs.StringVar(&opts.ConfigPath, "config", "", "path to user config")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "preview changes without writing files")
	fs.StringVar(&opts.BackupExt, "backup-ext", ".bak", "backup file extension")
	fs.BoolVar(&opts.Recursive, "recursive", false, "recursively convert file:// subcharts")
	fs.BoolVar(&opts.IncludeChartsDir, "include-charts-dir", false, "include subcharts in charts/ directory")
	fs.BoolVar(&opts.ExpandRemote, "expand-remote", false, "expand and process .tgz files in charts/")
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
      --backup-ext string    backup file extension (default: ".bak")
      --chart string         path to chart root (default: current directory)
      --config string        path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
      --dry-run              preview changes without writing files
      --expand-remote        expand and process .tgz files in charts/
  -h, --help                 help for convert
      --include-charts-dir   include subcharts in charts/ directory
      --recursive            recursively convert file:// subcharts and update umbrella values

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

  # Convert including embedded charts/ subcharts
  helm list-to-map convert --chart ./umbrella-chart --include-charts-dir

  # Convert all dependency types (use with caution for --expand-remote)
  helm list-to-map convert --chart ./umbrella-chart --recursive --include-charts-dir --expand-remote
`)
	}
	_ = fs.Parse(os.Args[2:])
	return runConvert(opts)
}

func runLoadCRDCommand() error {
	fs := flag.NewFlagSet("load-crd", flag.ExitOnError)
	opts := LoadCRDOptions{}
	fs.BoolVar(&opts.Force, "force", false, "overwrite existing CRD files")
	fs.BoolVar(&opts.Common, "common", false, "load CRDs from bundled crd-sources.yaml")
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
	opts.Sources = fs.Args()
	return runLoadCRD(opts)
}

func runListCRDsCommand() error {
	fs := flag.NewFlagSet("list-crds", flag.ExitOnError)
	opts := ListCRDsOptions{}
	fs.BoolVar(&opts.Verbose, "v", false, "show all convertible fields for each CRD")
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
	return runListCRDs(opts)
}

func runAddRuleCommand() error {
	fs := flag.NewFlagSet("add-rule", flag.ExitOnError)
	opts := AddRuleOptions{}
	fs.StringVar(&opts.Path, "path", "", "dot path to array (end with [])")
	fs.StringVar(&opts.UniqueKey, "uniqueKey", "", "unique key field")
	fs.StringVar(&opts.ConfigPath, "config", "", "path to user config")
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
	return runAddRule(opts)
}

func runListRulesCommand() error {
	fs := flag.NewFlagSet("rules", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Print(`
List custom conversion rules for CRDs and custom resources.

Note: Built-in K8s types are detected automatically via API introspection
and do not require rules. Use 'detect' to see what will be converted.

Usage:
  helm list-to-map rules [flags]

Flags:
  -h, --help   help for rules
`)
	}
	_ = fs.Parse(os.Args[2:])
	return runListRules(ListRulesOptions{})
}
