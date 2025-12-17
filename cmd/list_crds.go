package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

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
