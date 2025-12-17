package main

import (
	"fmt"
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
