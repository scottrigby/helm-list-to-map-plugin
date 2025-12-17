package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pkgfs "github.com/scottrigby/helm-list-to-map-plugin/pkg/fs"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/k8s"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/template"
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/transform"
)

func runConvert(opts ConvertOptions) error {
	root, err := findChartRoot(opts.ChartDir)
	if err != nil {
		return err
	}

	// Handle recursive conversion of umbrella charts
	if opts.Recursive {
		return runRecursiveConvert(root, opts)
	}

	// Local variable to track converted paths
	var transformedPaths []template.PathInfo

	// Load CRDs from plugin config directory
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: loading CRDs: %v\n", err)
	}

	// Use new programmatic detection via K8s API introspection
	candidates, err := k8s.DetectConversionCandidates(root)
	if err != nil {
		return err
	}

	// Also check for user-defined rules (for CRDs)
	userDetected := scanForUserRules(root)
	candidates = append(candidates, userDetected...)

	// Build PathInfo list and check which paths have matching template patterns
	var pathInfos []template.PathInfo
	for _, c := range candidates {
		pathInfos = append(pathInfos, template.PathInfo{
			DotPath:     c.ValuesPath,
			MergeKey:    c.MergeKey,
			SectionName: c.SectionName,
		})
	}

	// Check template patterns BEFORE converting values
	// Only convert values for paths where template patterns actually match
	matchedPaths := template.CheckTemplatePatterns(root, pathInfos)

	// Filter candidates to only include paths with matching template patterns
	candidateMap := make(map[string]k8s.DetectedCandidate)
	var skippedPaths []string
	for _, c := range candidates {
		if matchedPaths[c.ValuesPath] {
			candidateMap[c.ValuesPath] = c
		} else {
			skippedPaths = append(skippedPaths, c.ValuesPath)
		}
	}

	// Check values.yaml existence for candidates with matching templates
	var candidateList []k8s.DetectedCandidate
	for _, c := range candidateMap {
		candidateList = append(candidateList, c)
	}
	candidateList = k8s.CheckCandidatesInValues(root, candidateList)

	// Separate by values existence
	var withValuesCandidates, templateOnlyCandidates []k8s.DetectedCandidate
	for _, c := range candidateList {
		if c.ExistsInValues {
			withValuesCandidates = append(withValuesCandidates, c)
		} else {
			templateOnlyCandidates = append(templateOnlyCandidates, c)
		}
	}

	// Rebuild candidateMap with only candidates that have values
	candidateMap = make(map[string]k8s.DetectedCandidate)
	for _, c := range withValuesCandidates {
		candidateMap[c.ValuesPath] = c
	}

	// Warn about paths that couldn't be converted
	if len(skippedPaths) > 0 {
		fmt.Println("\nSkipped (template pattern not supported):")
		for _, p := range skippedPaths {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println("  These paths use inline append patterns (e.g., static entries + toYaml)")
		fmt.Println("  that cannot be automatically converted.")
	}

	valuesPath := filepath.Join(root, "values.yaml")
	doc, raw, err := loadValuesNode(valuesPath)
	if err != nil {
		return err
	}

	// Use line-based editing to preserve original formatting
	var edits []transform.ArrayEdit
	transform.FindArrayEdits(doc, nil, candidateMap, &edits)

	// Track all backup files created
	var backupFiles []string

	if len(edits) > 0 {
		out := transform.ApplyLineEdits(raw, edits)

		if opts.DryRun {
			fmt.Println("=== values.yaml (updated preview) ===")
			fmt.Println(string(out))
		} else {
			backupPath := valuesPath + opts.BackupExt
			if err := backupFile(valuesPath, opts.BackupExt, raw); err != nil {
				return err
			}
			backupFiles = append(backupFiles, backupPath)
			if err := os.WriteFile(valuesPath, out, 0644); err != nil {
				return err
			}
		}

		// Check if any env vars are being converted
		hasEnvVars := false
		for _, edit := range edits {
			if strings.Contains(edit.Candidate.ElementType, "EnvVar") ||
				strings.HasSuffix(edit.Candidate.ValuesPath, ".env") ||
				strings.HasSuffix(edit.Candidate.ValuesPath, "Env") {
				hasEnvVars = true
				break
			}
		}

		// Report changes with detailed info
		fmt.Println("\nConverted values.yaml fields:")
		for _, edit := range edits {
			// Build JSONPath for display
			jsonPath := edit.Candidate.YAMLPath
			if edit.Candidate.ResourceKind != "" {
				jsonPath = edit.Candidate.ResourceKind + "." + jsonPath
			}

			// Count items
			itemCount := 0
			transform.WalkForCount(doc, edit.Candidate.ValuesPath, &itemCount)

			// Display detailed info
			fmt.Printf("  %s:\n", edit.Candidate.ValuesPath)
			fmt.Printf("    JSONPath: %s\n", jsonPath)
			fmt.Printf("    Key:      %s\n", edit.Candidate.MergeKey)
			if edit.Candidate.ElementType != "" {
				fmt.Printf("    Type:     %s\n", edit.Candidate.ElementType)
			}
			if edit.Candidate.TemplateFile != "" {
				fmt.Printf("    Used in:  templates/%s\n", edit.Candidate.TemplateFile)
			}
			if itemCount == 0 {
				fmt.Printf("    Items:    0 (empty array)\n")
			} else {
				fmt.Printf("    Items:    %d\n", itemCount)
			}

			transformedPaths = append(transformedPaths, template.PathInfo{
				DotPath:     edit.Candidate.ValuesPath,
				MergeKey:    edit.Candidate.MergeKey,
				SectionName: edit.Candidate.SectionName,
			})
		}

		// Warn about env var ordering if applicable
		if hasEnvVars {
			fmt.Println("\n  WARNING: Environment variables will be rendered in alphabetical order.")
			fmt.Println("  If any env var uses $(OTHER_VAR) syntax to reference another env var,")
			fmt.Println("  ensure the referenced var comes BEFORE it alphabetically, or the")
			fmt.Println("  reference will fail. See 'helm list-to-map --help' for details.")
		}
	} else {
		fmt.Println("No changes needed in values.yaml.")
	}

	// Add template-only candidates to transformedPaths for template rewriting
	if len(templateOnlyCandidates) > 0 {
		fmt.Println("\nTemplate-only conversions (no values.yaml entry):")
		for _, c := range templateOnlyCandidates {
			fmt.Printf("  %s (key=%s)\n", c.ValuesPath, c.MergeKey)
			transformedPaths = append(transformedPaths, template.PathInfo{
				DotPath:     c.ValuesPath,
				MergeKey:    c.MergeKey,
				SectionName: c.SectionName,
			})
		}
		fmt.Println("\n  NOTE: These templates will be updated to use map-style syntax.")
		fmt.Println("  Please manually update any comments in values.yaml or documentation")
		fmt.Println("  that describe these fields to use map format instead of list format.")
	}

	var tchanges []string
	var helperCreated bool
	if !opts.DryRun {
		var err error
		tchanges, backupFiles, err = template.RewriteTemplatesWithBackups(pkgfs.OSFileSystem{}, root, transformedPaths, opts.BackupExt, backupFiles)
		if err != nil {
			return err
		}

		if len(tchanges) > 0 {
			fmt.Println("\nUpdated templates:")
			for _, ch := range tchanges {
				fmt.Printf("  %s\n", ch)
			}
		}

		helperCreated = template.EnsureHelpersWithReport(pkgfs.OSFileSystem{}, root)
		if helperCreated {
			fmt.Println("\nCreated helper template:")
			fmt.Printf("  templates/_listmap.tpl\n")
		}
	} else if len(transformedPaths) > 0 {
		fmt.Println("\nTemplate changes (dry-run, not applied):")
		for _, p := range transformedPaths {
			fmt.Printf("  Would update templates using .Values.%s\n", p.DotPath)
		}
		fmt.Println("  Would create templates/_listmap.tpl (if not exists)")
	}

	// Report backup files
	if !opts.DryRun && len(backupFiles) > 0 {
		fmt.Println("\nBackup files created:")
		for _, bf := range backupFiles {
			relPath, _ := filepath.Rel(root, bf)
			if relPath == "" {
				relPath = bf
			}
			fmt.Printf("  %s\n", relPath)
		}
	}

	if len(edits) == 0 && len(tchanges) == 0 && len(templateOnlyCandidates) == 0 && !opts.DryRun {
		fmt.Println("Nothing to convert.")
	}

	return nil
}

// convertSubchartAndTrack converts a subchart and returns the converted paths
func convertSubchartAndTrack(subchartPath string, opts ConvertOptions) (*SubchartConversion, error) {
	// Local variable to track converted paths
	var transformedPaths []template.PathInfo

	// Load CRDs from plugin config directory
	if err := loadCRDsFromConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: loading CRDs: %v\n", err)
	}

	// Use programmatic detection via K8s API introspection
	candidates, err := k8s.DetectConversionCandidates(subchartPath)
	if err != nil {
		return nil, fmt.Errorf("detecting candidates: %w", err)
	}

	// Also check for user-defined rules (for CRDs)
	userDetected := scanForUserRules(subchartPath)
	candidates = append(candidates, userDetected...)

	// Build PathInfo list and check which paths have matching template patterns
	var pathInfos []template.PathInfo
	for _, c := range candidates {
		pathInfos = append(pathInfos, template.PathInfo{
			DotPath:     c.ValuesPath,
			MergeKey:    c.MergeKey,
			SectionName: c.SectionName,
		})
	}

	// Check template patterns BEFORE converting values
	matchedPaths := template.CheckTemplatePatterns(subchartPath, pathInfos)

	// Filter candidates to only include paths with matching template patterns
	candidateMap := make(map[string]k8s.DetectedCandidate)
	for _, c := range candidates {
		if matchedPaths[c.ValuesPath] {
			candidateMap[c.ValuesPath] = c
		}
	}

	valuesPath := filepath.Join(subchartPath, "values.yaml")
	doc, raw, err := loadValuesNode(valuesPath)
	if err != nil {
		return nil, fmt.Errorf("loading values.yaml: %w", err)
	}

	// Use line-based editing to preserve original formatting
	var edits []transform.ArrayEdit
	transform.FindArrayEdits(doc, nil, candidateMap, &edits)

	if len(edits) > 0 {
		out := transform.ApplyLineEdits(raw, edits)

		if !opts.DryRun {
			backupPath := valuesPath + opts.BackupExt
			if err := backupFile(valuesPath, opts.BackupExt, raw); err != nil {
				return nil, fmt.Errorf("backing up values.yaml: %w", err)
			}
			fmt.Printf("    Backup: %s\n", backupPath)
			if err := os.WriteFile(valuesPath, out, 0644); err != nil {
				return nil, fmt.Errorf("writing values.yaml: %w", err)
			}
		}

		// Track converted paths
		for _, edit := range edits {
			transformedPaths = append(transformedPaths, template.PathInfo{
				DotPath:     edit.Candidate.ValuesPath,
				MergeKey:    edit.Candidate.MergeKey,
				SectionName: edit.Candidate.SectionName,
			})
		}
	}

	// Rewrite templates
	if !opts.DryRun && len(transformedPaths) > 0 {
		tchanges, _, err := template.RewriteTemplatesWithBackups(pkgfs.OSFileSystem{}, subchartPath, transformedPaths, opts.BackupExt, nil)
		if err != nil {
			return nil, fmt.Errorf("rewriting templates: %w", err)
		}
		for _, ch := range tchanges {
			fmt.Printf("    Updated template: %s\n", ch)
		}

		// Create helper template
		if template.EnsureHelpersWithReport(pkgfs.OSFileSystem{}, subchartPath) {
			fmt.Printf("    Created: templates/_listmap.tpl\n")
		}
	}

	// Return conversion info
	chartName := filepath.Base(subchartPath)
	return &SubchartConversion{
		Name:           chartName,
		ConvertedPaths: transformedPaths,
	}, nil
}

// updateUmbrellaValues updates the umbrella chart's values.yaml to convert arrays to maps
// for paths that were converted in subcharts
func updateUmbrellaValues(umbrellaRoot string, conversions []SubchartConversion, opts ConvertOptions) error {
	valuesPath := filepath.Join(umbrellaRoot, "values.yaml")
	doc, raw, err := loadValuesNode(valuesPath)
	if err != nil {
		return fmt.Errorf("loading umbrella values.yaml: %w", err)
	}

	// Build a map of subchart prefixed paths to their conversion info
	// e.g., "judge-api.deployment.env" -> PathInfo{MergeKey: "name", ...}
	subchartPaths := make(map[string]template.PathInfo)
	for _, conv := range conversions {
		for _, p := range conv.ConvertedPaths {
			// Prefix with subchart name
			prefixedPath := conv.Name + "." + p.DotPath
			subchartPaths[prefixedPath] = p
		}
	}

	// Find arrays in umbrella values that match subchart converted paths
	candidateMap := make(map[string]k8s.DetectedCandidate)
	for path, info := range subchartPaths {
		candidateMap[path] = k8s.DetectedCandidate{
			ValuesPath:  path,
			MergeKey:    info.MergeKey,
			SectionName: info.SectionName,
		}
	}

	// Find array edits in umbrella values
	var edits []transform.ArrayEdit
	transform.FindArrayEdits(doc, nil, candidateMap, &edits)

	if len(edits) == 0 {
		fmt.Println("\nNo umbrella values.yaml updates needed.")
		return nil
	}

	// Apply edits
	out := transform.ApplyLineEdits(raw, edits)

	if opts.DryRun {
		fmt.Println("\n=== Umbrella values.yaml updates (dry-run) ===")
		for _, edit := range edits {
			fmt.Printf("  Would convert: %s\n", edit.Candidate.ValuesPath)
		}
	} else {
		backupPath := valuesPath + opts.BackupExt
		if err := backupFile(valuesPath, opts.BackupExt, raw); err != nil {
			return fmt.Errorf("backing up umbrella values.yaml: %w", err)
		}
		if err := os.WriteFile(valuesPath, out, 0644); err != nil {
			return fmt.Errorf("writing umbrella values.yaml: %w", err)
		}

		fmt.Println("\nUpdated umbrella values.yaml:")
		fmt.Printf("  Backup: %s\n", backupPath)
		for _, edit := range edits {
			fmt.Printf("  Converted: %s (key=%s)\n", edit.Candidate.ValuesPath, edit.Candidate.MergeKey)
		}
	}

	return nil
}

// runRecursiveConvert handles the --recursive flag for umbrella charts
// It converts all file:// subcharts and then updates the umbrella values.yaml
func runRecursiveConvert(umbrellaRoot string, opts ConvertOptions) error {
	fmt.Printf("Recursive conversion for umbrella chart: %s\n", umbrellaRoot)

	// Parse Chart.yaml to find file:// dependencies
	deps, err := parseChartDependencies(umbrellaRoot)
	if err != nil {
		return fmt.Errorf("parsing dependencies: %w", err)
	}

	if len(deps) == 0 {
		fmt.Println("No file:// dependencies found in Chart.yaml.")
		fmt.Println("Use --recursive only for umbrella charts with local subcharts.")
		return nil
	}

	fmt.Printf("\nFound %d file:// subchart(s):\n", len(deps))
	for _, dep := range deps {
		fmt.Printf("  - %s (%s)\n", dep.Name, dep.Repository)
	}

	// Convert each subchart
	var conversions []SubchartConversion
	for _, dep := range deps {
		subchartPath := resolveSubchartPath(umbrellaRoot, dep.Repository)

		// Check if subchart exists
		if _, err := os.Stat(filepath.Join(subchartPath, "Chart.yaml")); err != nil {
			fmt.Fprintf(os.Stderr, "\nWarning: Subchart %s not found at %s, skipping\n", dep.Name, subchartPath)
			continue
		}

		fmt.Printf("\n=== Converting subchart: %s ===\n", dep.Name)
		fmt.Printf("  Path: %s\n", subchartPath)

		conv, err := convertSubchartAndTrack(subchartPath, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
			continue
		}

		if len(conv.ConvertedPaths) == 0 {
			fmt.Println("  No conversions needed")
		} else {
			fmt.Printf("  Converted %d path(s):\n", len(conv.ConvertedPaths))
			for _, p := range conv.ConvertedPaths {
				fmt.Printf("    - %s (key=%s)\n", p.DotPath, p.MergeKey)
			}
			conversions = append(conversions, *conv)
		}
	}

	// Update umbrella values.yaml with converted subchart paths
	if len(conversions) > 0 {
		fmt.Printf("\n=== Updating umbrella values.yaml ===\n")
		if err := updateUmbrellaValues(umbrellaRoot, conversions, opts); err != nil {
			return err
		}
	} else {
		fmt.Println("\nNo subcharts were converted, umbrella values.yaml unchanged.")
	}

	// Summary
	fmt.Println("\n=== Conversion Summary ===")
	totalPaths := 0
	for _, conv := range conversions {
		totalPaths += len(conv.ConvertedPaths)
	}
	fmt.Printf("Subcharts converted: %d\n", len(conversions))
	fmt.Printf("Total paths converted: %d\n", totalPaths)

	if !opts.DryRun {
		fmt.Println("\nNote: Run 'helm dependency build' to rebuild chart dependencies.")
	}

	return nil
}
