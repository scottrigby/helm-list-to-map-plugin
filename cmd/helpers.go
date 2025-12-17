package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

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
	return "", fmt.Errorf("chart.yaml not found starting from %s", start)
}

// parseChartDependencies reads Chart.yaml and returns file:// dependencies
func parseChartDependencies(chartRoot string) ([]ChartDependency, error) {
	chartPath := filepath.Join(chartRoot, "Chart.yaml")
	data, err := os.ReadFile(chartPath)
	if err != nil {
		return nil, fmt.Errorf("reading Chart.yaml: %w", err)
	}

	var chart ChartYAML
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return nil, fmt.Errorf("parsing Chart.yaml: %w", err)
	}

	// Filter to only file:// dependencies
	var fileDeps []ChartDependency
	for _, dep := range chart.Dependencies {
		if strings.HasPrefix(dep.Repository, "file://") {
			fileDeps = append(fileDeps, dep)
		}
	}

	return fileDeps, nil
}

// resolveSubchartPath resolves a file:// repository reference to an absolute path
func resolveSubchartPath(umbrellaRoot, repository string) string {
	// Remove file:// prefix
	relPath := strings.TrimPrefix(repository, "file://")
	// Resolve relative to umbrella chart root
	return filepath.Join(umbrellaRoot, relPath)
}

// loadValuesNode loads values.yaml as a yaml.Node tree to preserve comments and formatting
func loadValuesNode(path string) (*yaml.Node, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	return &doc, data, nil
}

func backupFile(path, ext string, original []byte) error {
	return os.WriteFile(path+ext, original, 0644)
}

// matchRule checks if a path matches any user-defined rule (for CRDs)
func matchRule(path []string) *Rule {
	dp := strings.Join(path, ".") + "[]"
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

// getLastPathSegment returns the last segment of a dot-separated path
// e.g., "spec.template.volumes" -> "volumes"
func getLastPathSegment(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

// SubchartInfo represents a subchart to be processed
type SubchartInfo struct {
	Name         string // subchart name (directory name or Chart.yaml name)
	Path         string // absolute path to subchart
	Source       string // "file://", "charts/", or "remote"
	RemoteSource string // repository URL (for remote charts)
	WasExpanded  bool   // true if extracted from .tgz
}

// scanChartsDirectory scans the charts/ directory for embedded subcharts
// Returns subcharts found as directories containing Chart.yaml
func scanChartsDirectory(chartRoot string) ([]SubchartInfo, error) {
	chartsDir := filepath.Join(chartRoot, "charts")

	// Check if charts/ exists
	if _, err := os.Stat(chartsDir); os.IsNotExist(err) {
		return nil, nil // Not an error, just no charts/
	}

	entries, err := os.ReadDir(chartsDir)
	if err != nil {
		return nil, fmt.Errorf("reading charts/ directory: %w", err)
	}

	var subcharts []SubchartInfo
	for _, entry := range entries {
		// Skip non-directories and .tgz files
		if !entry.IsDir() {
			continue
		}

		subchartPath := filepath.Join(chartsDir, entry.Name())
		chartYamlPath := filepath.Join(subchartPath, "Chart.yaml")

		// Check if this directory contains Chart.yaml
		if _, err := os.Stat(chartYamlPath); err == nil {
			subcharts = append(subcharts, SubchartInfo{
				Name:   entry.Name(),
				Path:   subchartPath,
				Source: "charts/",
			})
		}
	}

	return subcharts, nil
}

// scanChartsTarballs scans the charts/ directory for .tgz files
func scanChartsTarballs(chartRoot string) ([]string, error) {
	chartsDir := filepath.Join(chartRoot, "charts")

	// Check if charts/ exists
	if _, err := os.Stat(chartsDir); os.IsNotExist(err) {
		return nil, nil // Not an error, just no charts/
	}

	entries, err := os.ReadDir(chartsDir)
	if err != nil {
		return nil, fmt.Errorf("reading charts/ directory: %w", err)
	}

	var tarballs []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tgz") {
			tarballs = append(tarballs, filepath.Join(chartsDir, entry.Name()))
		}
	}

	return tarballs, nil
}

// collectSubcharts gathers all subcharts to process based on flags
// Handles file:// deps (--recursive), charts/ dirs (--include-charts-dir), and .tgz files (--expand-remote)
// Deduplicates by absolute path
func collectSubcharts(chartRoot string, recursive, includeChartsDir, expandRemote bool) ([]SubchartInfo, error) {
	subchartMap := make(map[string]SubchartInfo) // key: absolute path

	// Collect file:// dependencies from Chart.yaml
	if recursive {
		deps, err := parseChartDependencies(chartRoot)
		if err != nil {
			return nil, fmt.Errorf("parsing Chart.yaml dependencies: %w", err)
		}

		for _, dep := range deps {
			subchartPath := resolveSubchartPath(chartRoot, dep.Repository)
			// Make absolute for deduplication
			absPath, err := filepath.Abs(subchartPath)
			if err != nil {
				absPath = subchartPath
			}

			subchartMap[absPath] = SubchartInfo{
				Name:   dep.Name,
				Path:   absPath,
				Source: "file://",
			}
		}
	}

	// Collect embedded subcharts from charts/ directory
	if includeChartsDir {
		embedded, err := scanChartsDirectory(chartRoot)
		if err != nil {
			return nil, fmt.Errorf("scanning charts/ directory: %w", err)
		}

		for _, sub := range embedded {
			// Make absolute for deduplication
			absPath, err := filepath.Abs(sub.Path)
			if err != nil {
				absPath = sub.Path
			}

			// If already exists (from file:// dep), mark as "charts/ (via Chart.yaml)"
			if existing, exists := subchartMap[absPath]; exists {
				existing.Source = "charts/ (via Chart.yaml)"
				subchartMap[absPath] = existing
			} else {
				sub.Path = absPath
				subchartMap[absPath] = sub
			}
		}
	}

	// Extract and collect remote tarballs
	if expandRemote {
		tarballs, err := scanChartsTarballs(chartRoot)
		if err != nil {
			return nil, fmt.Errorf("scanning for tarballs: %w", err)
		}

		for _, tgzPath := range tarballs {
			extractedPath, repoURL, err := extractTarball(tgzPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to extract %s: %v\n", filepath.Base(tgzPath), err)
				continue
			}

			// Make absolute for deduplication
			absPath, err := filepath.Abs(extractedPath)
			if err != nil {
				absPath = extractedPath
			}

			// Extract name from tarball filename (remove .tgz)
			name := strings.TrimSuffix(filepath.Base(tgzPath), ".tgz")

			subchartMap[absPath] = SubchartInfo{
				Name:         name,
				Path:         absPath,
				Source:       "remote",
				RemoteSource: repoURL,
				WasExpanded:  true,
			}
		}
	}

	// Convert map to slice
	var subcharts []SubchartInfo
	for _, sub := range subchartMap {
		subcharts = append(subcharts, sub)
	}

	return subcharts, nil
}

// extractTarball extracts a .tgz file to a directory in the same location
// Returns the extracted directory path and repository URL from Chart.yaml
// Creates a backup of the original .tgz file
func extractTarball(tgzPath string) (string, string, error) {
	// Create backup of .tgz
	backupPath := tgzPath + ".bak"
	tgzData, err := os.ReadFile(tgzPath)
	if err != nil {
		return "", "", fmt.Errorf("reading tarball: %w", err)
	}
	if err := os.WriteFile(backupPath, tgzData, 0644); err != nil {
		return "", "", fmt.Errorf("creating backup: %w", err)
	}

	// Open tarball
	f, err := os.Open(tgzPath)
	if err != nil {
		return "", "", fmt.Errorf("opening tarball: %w", err)
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return "", "", fmt.Errorf("creating gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)

	// Extract to directory with same name as tarball (minus .tgz)
	extractDir := strings.TrimSuffix(tgzPath, ".tgz")
	var chartYamlContent []byte

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("reading tar header: %w", err)
		}

		// Remove leading directory from tar path (helm packages include chart name as root dir)
		targetPath := header.Name
		parts := strings.SplitN(targetPath, "/", 2)
		if len(parts) == 2 {
			targetPath = parts[1]
		} else {
			continue // Skip root directory entry
		}

		target := filepath.Join(extractDir, targetPath)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return "", "", fmt.Errorf("creating directory %s: %w", target, err)
			}
		case tar.TypeReg:
			// Create parent directory if needed
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return "", "", fmt.Errorf("creating parent directory for %s: %w", target, err)
			}

			// Extract file
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return "", "", fmt.Errorf("creating file %s: %w", target, err)
			}

			if _, err := io.Copy(outFile, tr); err != nil {
				_ = outFile.Close()
				return "", "", fmt.Errorf("extracting file %s: %w", target, err)
			}
			_ = outFile.Close()

			// Capture Chart.yaml content for repo URL extraction
			if targetPath == "Chart.yaml" {
				chartYamlContent, _ = os.ReadFile(target)
			}
		}
	}

	// Remove original .tgz file
	if err := os.Remove(tgzPath); err != nil {
		return "", "", fmt.Errorf("removing original tarball: %w", err)
	}

	// Extract repository URL from Chart.yaml
	repoURL := ""
	if len(chartYamlContent) > 0 {
		var chart ChartYAML
		if err := yaml.Unmarshal(chartYamlContent, &chart); err == nil {
			// Try to find repository in annotations or sources
			if chart.Annotations != nil {
				if repo, ok := chart.Annotations["repository"]; ok {
					repoURL = repo
				}
			}
			if repoURL == "" && len(chart.Sources) > 0 {
				repoURL = chart.Sources[0]
			}
		}
	}

	return extractDir, repoURL, nil
}

// displayRemoteWarning displays a prominent warning about converted remote dependencies
func displayRemoteWarning(expandedCharts []SubchartInfo) {
	if len(expandedCharts) == 0 {
		return
	}

	fmt.Println()
	fmt.Println("┌─────────────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ WARNING: Converting remote dependencies                                 │")
	fmt.Println("│                                                                          │")
	fmt.Println("│ The following dependencies were expanded from tarballs and converted:   │")
	for _, chart := range expandedCharts {
		if chart.RemoteSource != "" {
			fmt.Printf("│   - %-65s │\n", fmt.Sprintf("%s (%s)", chart.Name, chart.RemoteSource))
		} else {
			fmt.Printf("│   - %-65s │\n", chart.Name)
		}
	}
	fmt.Println("│                                                                          │")
	fmt.Println("│ These changes will be LOST if you run 'helm dependency update'.          │")
	fmt.Println("│                                                                          │")
	fmt.Println("│ Recommended actions:                                                     │")
	fmt.Println("│   - If you own the source: convert the chart at its source repository   │")
	fmt.Println("│   - If community chart: file an issue requesting map-based values       │")
	fmt.Println("│   - Or: fork the chart and use file:// dependency                       │")
	fmt.Println("│                                                                          │")
	fmt.Println("│ Backups created: <tarball>.tgz.bak                                       │")
	fmt.Println("└─────────────────────────────────────────────────────────────────────────┘")
	fmt.Println()
}
