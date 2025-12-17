package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

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
