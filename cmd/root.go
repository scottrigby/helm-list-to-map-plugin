// Build to: bin/list-to-map
package main

import (
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

// Global variables (will be replaced with Options structs in Phase 5)
var (
	subcmd           string
	chartDir         string
	dryRun           bool
	backupExt        string
	configPath       string
	recursive        bool
	conf             Config
	transformedPaths []template.PathInfo
)

// Global flag for force overwrite (set by runLoadCRD)
var forceOverwrite bool

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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
