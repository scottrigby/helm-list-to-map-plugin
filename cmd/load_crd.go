package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/crd"
	pkgfs "github.com/scottrigby/helm-list-to-map-plugin/pkg/fs"
)

func runLoadCRD(opts LoadCRDOptions) error {
	// Handle --common flag
	if opts.Common {
		return loadCommonCRDs()
	}

	if len(opts.Sources) == 0 {
		return fmt.Errorf("at least one CRD source is required (or use --common)")
	}

	// Ensure CRD config directory exists
	crdsDir := crdConfigDir()
	if err := os.MkdirAll(crdsDir, 0755); err != nil {
		return fmt.Errorf("creating CRD directory: %w", err)
	}

	// Process each source
	for _, source := range opts.Sources {
		if err := loadAndStoreCRD(source, crdsDir, opts.Force); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", source, err)
			continue
		}
	}

	return nil
}

// loadCommonCRDs loads CRDs from the bundled common-crds.yaml file
func loadCommonCRDs() error {
	// Find common-crds.yaml in plugin directory
	pluginDir := os.Getenv("HELM_PLUGIN_DIR")
	if pluginDir == "" {
		// Fallback: check current directory and parent
		candidates := []string{"common-crds.yaml", "../common-crds.yaml"}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				pluginDir = filepath.Dir(c)
				break
			}
		}
	}

	sourcesFile := filepath.Join(pluginDir, "common-crds.yaml")
	if _, err := os.Stat(sourcesFile); err != nil {
		// Try current directory as fallback
		sourcesFile = "common-crds.yaml"
	}

	sources, err := crd.LoadCRDSources(sourcesFile)
	if err != nil {
		return fmt.Errorf("loading common-crds.yaml: %w", err)
	}

	// Ensure CRD config directory exists
	crdsDir := crdConfigDir()
	if err := os.MkdirAll(crdsDir, 0755); err != nil {
		return fmt.Errorf("creating CRD directory: %w", err)
	}

	fmt.Printf("Loading CRDs from bundled sources...\n\n")

	loaded := 0
	skipped := 0

	for group, entry := range sources {
		// Use entry's default_version, fallback to "main" if not specified
		version := entry.DefaultVersion
		if version == "" {
			version = "main"
		}

		url := entry.GetDownloadURL(version)
		if url == "" {
			if entry.Note != "" {
				fmt.Printf("  %s: skipped (%s)\n", group, entry.Note)
			} else {
				fmt.Printf("  %s: skipped (no direct URL, only url_pattern available)\n", group)
			}
			skipped++
			continue
		}

		fmt.Printf("  %s (version: %s)\n", group, version)
		fmt.Printf("    Source: %s\n", url)

		if err := loadAndStoreCRDFromURL(url, crdsDir, false); err != nil {
			fmt.Printf("    Error: %v\n", err)
			continue
		}
		loaded++
	}

	fmt.Printf("\nLoaded %d source(s), skipped %d\n", loaded, skipped)

	// Show what's now loaded
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	types := crd.GetGlobalRegistry().ListTypes()
	if len(types) > 0 {
		fmt.Printf("\nTotal CRD types available: %d\n", len(types))
	}

	return nil
}

// loadAndStoreCRD loads a CRD from file, directory, or URL and stores it in the config directory
func loadAndStoreCRD(source, crdsDir string, force bool) error {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		// Download from URL
		return loadAndStoreCRDFromURL(source, crdsDir, force)
	}

	// Check if source is a directory
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("accessing source: %w", err)
	}

	if info.IsDir() {
		// Load all CRD files from directory
		return loadAndStoreCRDsFromDirectory(source, crdsDir, force)
	}

	// Load single file
	return loadAndStoreCRDFromFile(source, crdsDir, force)
}

// loadAndStoreCRDFromURL downloads a CRD from a URL and stores it
func loadAndStoreCRDFromURL(url, crdsDir string, force bool) error {
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

	// Extract canonical filename from CRD metadata (includes storage version)
	filename, err := crd.ExtractCanonicalFilename(data)
	if err != nil {
		// Fallback to URL-based filename
		parts := strings.Split(url, "/")
		filename = parts[len(parts)-1]
		if filename == "" || !strings.HasSuffix(filename, ".yaml") {
			filename = "crd-" + fmt.Sprintf("%d", len(url)%10000) + ".yaml"
		}
	}

	destPath := filepath.Join(crdsDir, filename)

	// Check if file exists (skip unless --force)
	if exists, reason := crd.CRDFileExists(pkgfs.OSFileSystem{}, destPath); exists && !force {
		fmt.Printf("Skipped: %s -> %s (%s)\n", url, destPath, reason)
		return nil
	}

	// Write to config directory
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("writing to config: %w", err)
	}

	fmt.Printf("Loaded: %s -> %s\n", url, destPath)
	return nil
}

// loadAndStoreCRDFromFile loads a CRD from a file and stores it
func loadAndStoreCRDFromFile(source, crdsDir string, force bool) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	// Extract canonical filename from CRD metadata (includes storage version)
	// This also validates that the file contains a valid CRD
	filename, err := crd.ExtractCanonicalFilename(data)
	if err != nil {
		return fmt.Errorf("not a valid CRD: %w", err)
	}

	destPath := filepath.Join(crdsDir, filename)

	// Check if file exists (skip unless --force)
	if exists, reason := crd.CRDFileExists(pkgfs.OSFileSystem{}, destPath); exists && !force {
		fmt.Printf("Skipped: %s -> %s (%s)\n", source, destPath, reason)
		return nil
	}

	// Write to config directory
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("writing to config: %w", err)
	}

	fmt.Printf("Loaded: %s -> %s\n", source, destPath)
	return nil
}

// loadAndStoreCRDsFromDirectory loads all CRD YAML files from a directory
func loadAndStoreCRDsFromDirectory(sourceDir, crdsDir string, force bool) error {
	var loaded, skipped int
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

		// Try to load each YAML file as a CRD
		// Files that aren't valid CRDs are silently skipped
		if err := loadAndStoreCRDFromFile(path, crdsDir, force); err != nil {
			skipped++
			return nil
		}
		loaded++
		return nil
	})

	if err != nil {
		return err
	}

	if loaded == 0 {
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "Warning: no CRD files found in %s (%d YAML file(s) checked but none contained CRDs)\n", sourceDir, skipped)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: no YAML files found in %s\n", sourceDir)
		}
	} else {
		fmt.Printf("\nLoaded %d CRD file(s) from %s\n", loaded, sourceDir)
	}

	return nil
}

// loadCRDsFromConfig loads all CRD definitions from the plugin's config directory
func loadCRDsFromConfig() error {
	crdsDir := crdConfigDir()
	if info, err := os.Stat(crdsDir); err != nil || !info.IsDir() {
		// No CRDs directory - that's fine, just skip
		return nil
	}

	return crd.GetGlobalRegistry().LoadFromDirectory(crdsDir)
}
