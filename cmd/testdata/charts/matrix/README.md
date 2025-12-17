# Subchart Dependency Test Matrix Fixtures

This directory contains test fixtures for integration testing of subchart dependency features (--include-charts-dir, --expand-remote, --recursive flags).

## Test Matrix Implementation Status

### Completed Fixtures (5 scenarios)

| Test ID | Description | Location | Status |
|---------|-------------|----------|--------|
| S1-C | Single embedded chart in charts/ | `single-types/s1-charts/` | ✅ Complete |
| S1-T | Single tarball in charts/ | `single-types/s1-tarball/` | ✅ Complete |
| M2-FC | Mix of file:// + charts/ | `two-type-mix/m2-file-charts/` | ✅ Complete |
| D-FC | Deduplication test | `deduplication/d-file-to-charts/` | ✅ Complete |
| M3-FCT | All three types combined | `three-type-mix/m3-all/` | ✅ Complete |

### Fixture Details

**S1-C: Single Embedded Chart**
- Parent chart with one embedded subchart in `charts/embedded/`
- Subchart has env and volumes arrays to convert
- Tests: --include-charts-dir flag

**S1-T: Single Tarball**
- Parent chart with one .tgz tarball in `charts/`
- Tarball contains chart with env and volumeMounts arrays
- Tests: --expand-remote flag, backup creation, extraction

**M2-FC: Mixed File and Charts**
- Parent chart with file:// dependency + embedded chart
- Sibling chart (file://) has volumes arrays
- Embedded chart (charts/) has env arrays
- Tests: --recursive + --include-charts-dir flags together

**D-FC: Deduplication**
- Parent chart with file:// dependency pointing to `./charts/shared`
- Same chart accessible both ways (file:// and charts/)
- Tests: Deduplication logic (chart processed only once)

**M3-FCT: All Three Types**
- Parent with file:// dep + charts/ dir + tarball
- Sibling chart (file://) has ports arrays
- Embedded chart (charts/) has volumes arrays
- Tarball chart has volumeMounts arrays
- Tests: All three flags together

## Test Results

### ✅ All Tests Passing (8/8)

**Unit-Style Tests (3):**
- `TestSubchartS1C_DetectCount` ✅ - Verifies --include-charts-dir finds embedded charts
- `TestSubchartM2FC_DetectCount` ✅ - Verifies mixed file:// + charts/ detection
- `TestSubchartDFC_DeduplicationCount` ✅ - Verifies deduplication logic works

**Integration Tests (5):**
- `TestSubchartS1C_SingleEmbeddedChart` ✅ - S1-C scenario with --include-charts-dir
- `TestSubchartS1T_SingleTarball` ✅ - S1-T scenario with --expand-remote
- `TestSubchartM2FC_MixFileAndCharts` ✅ - M2-FC scenario with both flags
- `TestSubchartDFC_Deduplication` ✅ - D-FC scenario verifying deduplication
- `TestSubchartM3FCT_AllThreeTypes` ✅ - M3-FCT scenario with all three types

### Issues Resolved

**✅ Issue 1: Array Detection (FIXED)**
- **Root cause:** Template indentation was incorrect
- **Problem:** Helm directives like `{{- toYaml .Values.env | nindent 12 }}` need to be indented relative to their parent YAML key
- **Solution:** Fixed all fixture templates to use proper indentation:
  ```yaml
  # Correct:
  env:
    {{- toYaml .Values.env | nindent 12 }}

  # Incorrect (was causing detection to fail):
  env:
  {{- toYaml .Values.env | nindent 12 }}
  ```

**✅ Issue 2: Test Helper for file:// Dependencies (FIXED)**
- **Root cause:** `copyChart()` only copied the chart directory, not sibling charts
- **Problem:** Tests with file:// dependencies failed because sibling charts weren't in the copied structure
- **Solution:** Created `copyChartWithSiblings()` helper that copies the parent directory including all siblings
- Tests M2-FC and M3-FCT now use this helper

## Next Steps

### High Priority

1. **Investigate Detection Logic** - Why aren't arrays being detected in subcharts?
   - Check if K8s schema needs to be initialized
   - Verify built-in rules work with subchart detection
   - Test manually with: `./bin/list-to-map detect --chart testdata/charts/matrix/single-types/s1-charts --include-charts-dir -v`

2. **Fix Test Helper for file:// Dependencies**
   - Option A: Modify `copyChart()` to accept and copy sibling directories
   - Option B: Create test fixtures with self-contained structure (all deps inside chart dir)
   - Option C: Use absolute paths or symlinks in test

### Medium Priority

3. **Add More Nested Test Cases** (from TESTING_PLAN.md)
   - N2-F-F: file:// → file:// (two levels)
   - N2-F-C: file:// → charts/
   - N2-F-T: file:// → tarball

4. **Add S1-F Test** - Single file:// dependency (currently have S1-C and S1-T)

### Low Priority

5. **Performance Tests** - Verify large charts with many subcharts
6. **Error Case Tests** - Invalid tarballs, missing Chart.yaml, circular deps

## Running Tests

```bash
# Run all subchart tests
go test -v ./cmd/... -run TestSubchart

# Run only passing unit tests
go test -v ./cmd -run 'TestSubchart(S1C|M2FC|DFC)_DetectCount$'

# Run specific integration test
go test -v ./cmd -run TestSubchartS1C_SingleEmbeddedChart
```

## Fixture File Count

- 11 Chart.yaml files (1 parent + subcharts for each scenario)
- 2 .tgz tarballs (S1-T and M3-FCT scenarios)
- Multiple values.yaml and template files with test arrays
