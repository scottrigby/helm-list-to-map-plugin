package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanChartsDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(t *testing.T) string // Returns chart root
		wantCount int
		wantNames []string
	}{
		{
			name: "no charts directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				// Create Chart.yaml but no charts/ directory
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				return dir
			},
			wantCount: 0,
		},
		{
			name: "empty charts directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				_ = os.Mkdir(filepath.Join(dir, "charts"), 0755)
				return dir
			},
			wantCount: 0,
		},
		{
			name: "single embedded subchart",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				chartsDir := filepath.Join(dir, "charts")
				_ = os.Mkdir(chartsDir, 0755)

				// Create subchart
				subDir := filepath.Join(chartsDir, "subchart-a")
				_ = os.Mkdir(subDir, 0755)
				_ = os.WriteFile(filepath.Join(subDir, "Chart.yaml"), []byte("apiVersion: v2\nname: subchart-a\n"), 0644)

				return dir
			},
			wantCount: 1,
			wantNames: []string{"subchart-a"},
		},
		{
			name: "multiple embedded subcharts",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				chartsDir := filepath.Join(dir, "charts")
				_ = os.Mkdir(chartsDir, 0755)

				// Create multiple subcharts
				for _, name := range []string{"sub-a", "sub-b", "sub-c"} {
					subDir := filepath.Join(chartsDir, name)
					_ = os.Mkdir(subDir, 0755)
					_ = os.WriteFile(filepath.Join(subDir, "Chart.yaml"), []byte("apiVersion: v2\nname: "+name+"\n"), 0644)
				}

				return dir
			},
			wantCount: 3,
			wantNames: []string{"sub-a", "sub-b", "sub-c"},
		},
		{
			name: "ignores directories without Chart.yaml",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				chartsDir := filepath.Join(dir, "charts")
				_ = os.Mkdir(chartsDir, 0755)

				// Valid subchart
				subDir := filepath.Join(chartsDir, "valid")
				_ = os.Mkdir(subDir, 0755)
				_ = os.WriteFile(filepath.Join(subDir, "Chart.yaml"), []byte("apiVersion: v2\nname: valid\n"), 0644)

				// Invalid - no Chart.yaml
				invalidDir := filepath.Join(chartsDir, "invalid")
				_ = os.Mkdir(invalidDir, 0755)

				return dir
			},
			wantCount: 1,
			wantNames: []string{"valid"},
		},
		{
			name: "ignores .tgz files",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				chartsDir := filepath.Join(dir, "charts")
				_ = os.Mkdir(chartsDir, 0755)

				// Create .tgz file (should be ignored)
				_ = os.WriteFile(filepath.Join(chartsDir, "remote-chart.tgz"), []byte("fake tarball"), 0644)

				// Valid subchart
				subDir := filepath.Join(chartsDir, "local")
				_ = os.Mkdir(subDir, 0755)
				_ = os.WriteFile(filepath.Join(subDir, "Chart.yaml"), []byte("apiVersion: v2\nname: local\n"), 0644)

				return dir
			},
			wantCount: 1,
			wantNames: []string{"local"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chartRoot := tt.setup(t)

			subcharts, err := scanChartsDirectory(chartRoot)
			if err != nil {
				t.Fatalf("scanChartsDirectory() error = %v", err)
			}

			if len(subcharts) != tt.wantCount {
				t.Errorf("scanChartsDirectory() returned %d subcharts, want %d", len(subcharts), tt.wantCount)
			}

			// Verify names
			for _, wantName := range tt.wantNames {
				found := false
				for _, sub := range subcharts {
					if sub.Name == wantName {
						found = true
						// Verify source is correct
						if sub.Source != "charts/" {
							t.Errorf("subchart %s has source %q, want %q", sub.Name, sub.Source, "charts/")
						}
						// Verify path is set
						if sub.Path == "" {
							t.Errorf("subchart %s has empty path", sub.Name)
						}
						break
					}
				}
				if !found {
					t.Errorf("expected subchart %s not found", wantName)
				}
			}
		})
	}
}

func TestScanChartsTarballs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(t *testing.T) string // Returns chart root
		wantCount int
		wantFiles []string // Filenames (not full paths)
	}{
		{
			name: "no charts directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				return dir
			},
			wantCount: 0,
		},
		{
			name: "empty charts directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				_ = os.Mkdir(filepath.Join(dir, "charts"), 0755)
				return dir
			},
			wantCount: 0,
		},
		{
			name: "single tarball",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				chartsDir := filepath.Join(dir, "charts")
				_ = os.Mkdir(chartsDir, 0755)
				_ = os.WriteFile(filepath.Join(chartsDir, "mysql-1.0.0.tgz"), []byte("fake tarball"), 0644)
				return dir
			},
			wantCount: 1,
			wantFiles: []string{"mysql-1.0.0.tgz"},
		},
		{
			name: "multiple tarballs",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				chartsDir := filepath.Join(dir, "charts")
				_ = os.Mkdir(chartsDir, 0755)

				for _, name := range []string{"mysql-1.0.0.tgz", "redis-2.0.0.tgz", "postgres-3.0.0.tgz"} {
					_ = os.WriteFile(filepath.Join(chartsDir, name), []byte("fake tarball"), 0644)
				}

				return dir
			},
			wantCount: 3,
			wantFiles: []string{"mysql-1.0.0.tgz", "redis-2.0.0.tgz", "postgres-3.0.0.tgz"},
		},
		{
			name: "ignores directories",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				chartsDir := filepath.Join(dir, "charts")
				_ = os.Mkdir(chartsDir, 0755)

				// Create directory (should be ignored)
				subDir := filepath.Join(chartsDir, "local-chart")
				_ = os.Mkdir(subDir, 0755)

				// Create tarball
				_ = os.WriteFile(filepath.Join(chartsDir, "remote-1.0.0.tgz"), []byte("fake tarball"), 0644)

				return dir
			},
			wantCount: 1,
			wantFiles: []string{"remote-1.0.0.tgz"},
		},
		{
			name: "ignores non-.tgz files",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: test\n"), 0644)
				chartsDir := filepath.Join(dir, "charts")
				_ = os.Mkdir(chartsDir, 0755)

				// Create various non-.tgz files
				_ = os.WriteFile(filepath.Join(chartsDir, "README.md"), []byte("readme"), 0644)
				_ = os.WriteFile(filepath.Join(chartsDir, "chart.tar.gz"), []byte("wrong ext"), 0644)

				// Create valid tarball
				_ = os.WriteFile(filepath.Join(chartsDir, "valid-1.0.0.tgz"), []byte("fake tarball"), 0644)

				return dir
			},
			wantCount: 1,
			wantFiles: []string{"valid-1.0.0.tgz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chartRoot := tt.setup(t)

			tarballs, err := scanChartsTarballs(chartRoot)
			if err != nil {
				t.Fatalf("scanChartsTarballs() error = %v", err)
			}

			if len(tarballs) != tt.wantCount {
				t.Errorf("scanChartsTarballs() returned %d tarballs, want %d", len(tarballs), tt.wantCount)
			}

			// Verify filenames
			for _, wantFile := range tt.wantFiles {
				found := false
				for _, tgzPath := range tarballs {
					if filepath.Base(tgzPath) == wantFile {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected tarball %s not found", wantFile)
				}
			}
		})
	}
}

func TestCollectSubcharts_OnlyRecursive(t *testing.T) {
	t.Parallel()

	// Setup: chart with file:// dependency
	dir := t.TempDir()
	chartYaml := `apiVersion: v2
name: umbrella
dependencies:
  - name: subchart-a
    repository: file://../subchart-a
`
	_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0644)

	// Create subchart
	subchartDir := filepath.Join(filepath.Dir(dir), "subchart-a")
	_ = os.MkdirAll(subchartDir, 0755)
	_ = os.WriteFile(filepath.Join(subchartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: subchart-a\n"), 0644)

	// Test with only recursive flag
	subcharts, err := collectSubcharts(dir, true, false, false)
	if err != nil {
		t.Fatalf("collectSubcharts() error = %v", err)
	}

	if len(subcharts) != 1 {
		t.Fatalf("expected 1 subchart, got %d", len(subcharts))
	}

	if subcharts[0].Name != "subchart-a" {
		t.Errorf("expected name subchart-a, got %s", subcharts[0].Name)
	}

	if subcharts[0].Source != "file://" {
		t.Errorf("expected source file://, got %s", subcharts[0].Source)
	}
}

func TestCollectSubcharts_OnlyIncludeChartsDir(t *testing.T) {
	t.Parallel()

	// Setup: chart with embedded subchart in charts/
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: umbrella\n"), 0644)

	chartsDir := filepath.Join(dir, "charts")
	_ = os.Mkdir(chartsDir, 0755)

	subDir := filepath.Join(chartsDir, "embedded")
	_ = os.Mkdir(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "Chart.yaml"), []byte("apiVersion: v2\nname: embedded\n"), 0644)

	// Test with only include-charts-dir flag
	subcharts, err := collectSubcharts(dir, false, true, false)
	if err != nil {
		t.Fatalf("collectSubcharts() error = %v", err)
	}

	if len(subcharts) != 1 {
		t.Fatalf("expected 1 subchart, got %d", len(subcharts))
	}

	if subcharts[0].Name != "embedded" {
		t.Errorf("expected name embedded, got %s", subcharts[0].Name)
	}

	if subcharts[0].Source != "charts/" {
		t.Errorf("expected source charts/, got %s", subcharts[0].Source)
	}
}

func TestCollectSubcharts_Deduplication(t *testing.T) {
	t.Parallel()

	// Setup: chart with file:// dep pointing to charts/ directory
	dir := t.TempDir()
	chartYaml := `apiVersion: v2
name: umbrella
dependencies:
  - name: shared
    repository: file://./charts/shared
`
	_ = os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0644)

	chartsDir := filepath.Join(dir, "charts")
	_ = os.Mkdir(chartsDir, 0755)

	sharedDir := filepath.Join(chartsDir, "shared")
	_ = os.Mkdir(sharedDir, 0755)
	_ = os.WriteFile(filepath.Join(sharedDir, "Chart.yaml"), []byte("apiVersion: v2\nname: shared\n"), 0644)

	// Test with both flags (should deduplicate)
	subcharts, err := collectSubcharts(dir, true, true, false)
	if err != nil {
		t.Fatalf("collectSubcharts() error = %v", err)
	}

	if len(subcharts) != 1 {
		t.Fatalf("expected 1 deduplicated subchart, got %d", len(subcharts))
	}

	if subcharts[0].Name != "shared" {
		t.Errorf("expected name shared, got %s", subcharts[0].Name)
	}

	// Should be marked as "charts/ (via Chart.yaml)" due to deduplication
	if subcharts[0].Source != "charts/ (via Chart.yaml)" {
		t.Errorf("expected source 'charts/ (via Chart.yaml)', got %s", subcharts[0].Source)
	}
}
