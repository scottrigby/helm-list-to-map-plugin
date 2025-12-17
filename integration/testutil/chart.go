//go:build integration

package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// CopyChart copies a test chart to a temp directory for modification
func CopyChart(t *testing.T, srcChart string) string {
	t.Helper()
	dst := t.TempDir()

	err := filepath.Walk(srcChart, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(srcChart, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("failed to copy chart: %v", err)
	}
	return dst
}

// CopyChartWithSiblings copies a test chart and its sibling directories
func CopyChartWithSiblings(t *testing.T, srcChart string) string {
	t.Helper()

	parentDir := filepath.Dir(srcChart)
	chartName := filepath.Base(srcChart)
	dst := t.TempDir()

	err := filepath.Walk(parentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(parentDir, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("failed to copy chart with siblings: %v", err)
	}

	return filepath.Join(dst, chartName)
}

// GetTestdataPath returns the absolute path to a testdata file
func GetTestdataPath(t *testing.T, relativePath string) string {
	t.Helper()
	return filepath.Join("testdata", relativePath)
}
