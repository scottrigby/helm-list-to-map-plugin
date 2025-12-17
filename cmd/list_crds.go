package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/crd"
)

func runListCRDs(opts ListCRDsOptions) error {
	// Load CRDs from config
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	types := crd.GetGlobalRegistry().ListTypes()
	if len(types) == 0 {
		fmt.Println("No CRDs loaded.")
		fmt.Println("Use 'helm list-to-map load-crd <file-or-url>' to load CRD definitions.")
		return nil
	}

	if opts.Verbose {
		// Verbose: show each CRD with its fields
		fmt.Printf("Loaded CRD types (%d):\n", len(types))
		for _, t := range types {
			parts := strings.Split(t, "/")
			apiVersion, kind := parts[0], parts[1]
			fields := crd.GetGlobalRegistry().ListFields(apiVersion, kind)
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
			parts := strings.Split(t, "/")
			apiVersion, kind := parts[0], parts[1]
			fields := crd.GetGlobalRegistry().ListFields(apiVersion, kind)
			fmt.Printf("%-*s  %d\n", maxLen, t, len(fields))
		}

		fmt.Println("\nUse -v to see field details.")
	}

	return nil
}
