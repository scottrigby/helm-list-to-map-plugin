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
	fs.Usage = func() {
		fmt.Print(`
Scan a Helm chart to detect arrays that can be converted to maps based on
unique key fields. This is a read-only operation that reports potential conversions
without modifying any files.

Usage:
  helm list-to-map detect [flags]

Flags:
      --chart string    path to chart root (default: current directory)
      --config string   path to user config
  -h, --help            help for detect
      --recursive       recursively detect in file:// subcharts
  -v                    verbose output
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
	fs.Usage = func() {
		fmt.Print(`
Transform array-based configurations to map-based configurations in values.yaml
and automatically update corresponding template files.

Usage:
  helm list-to-map convert [flags]

Flags:
      --backup-ext string   backup file extension (default: ".bak")
      --chart string        path to chart root (default: current directory)
      --config string       path to user config
      --dry-run             preview changes without writing files
  -h, --help                help for convert
      --recursive           recursively convert file:// subcharts
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
fields in Custom Resources.

Usage:
  helm list-to-map load-crd [flags] <source> [source...]
  helm list-to-map load-crd --common

Flags:
      --common  load CRDs from bundled crd-sources.yaml
      --force   overwrite existing CRD files
  -h, --help    help for load-crd
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
  -v           verbose
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

Usage:
  helm list-to-map add-rule [flags]

Flags:
      --config string      path to user config
  -h, --help               help for add-rule
      --path string        dot path to array (end with [])
      --uniqueKey string   unique key field
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

Usage:
  helm list-to-map rules [flags]

Flags:
  -h, --help   help for rules
`)
	}
	_ = fs.Parse(os.Args[2:])
	return runListRules(ListRulesOptions{})
}
