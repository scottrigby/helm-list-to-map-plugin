package main

import (
	"fmt"
	"os"
)

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
