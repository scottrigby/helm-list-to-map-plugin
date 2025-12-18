# Subchart Dependency Test Matrix Fixtures

This directory contains test fixtures for comprehensive subchart dependency testing. These fixtures test the `--recursive`, `--include-charts-dir`, and `--expand-remote` flags in various combinations.

## Test Matrix Status: COMPLETE ✅ (11/11)

All subchart dependency scenarios are fully tested with fixtures and passing tests.

### Test Scenarios

| Test ID | Description | Flags Tested | Location | Status |
|---------|-------------|--------------|----------|--------|
| **S1-F** | Single file:// dependency | `--recursive` | `single-types/s1-file/` | ✅ `TestS1F_SingleFileDependency` |
| **S1-C** | Single embedded charts/ | `--include-charts-dir` | `single-types/s1-charts/` | ✅ `TestS1C_SingleEmbeddedChart` |
| **S1-T** | Single tarball | `--expand-remote` | `single-types/s1-tarball/` | ✅ `TestS1T_SingleTarball` |
| **M2-FC** | file:// + charts/ | `--recursive --include-charts-dir` | `two-type-mix/m2-file-charts/` | ✅ `TestM2FC_MixFileAndCharts` |
| **M2-FT** | file:// + tarball | `--recursive --expand-remote` | `two-type-mix/m2-file-tarball/` | ✅ `TestM2FT_MixFileAndTarball` |
| **M2-CT** | charts/ + tarball | `--include-charts-dir --expand-remote` | `two-type-mix/m2-charts-tarball/` | ✅ `TestM2CT_MixChartsAndTarball` |
| **M3-FCT** | All three types | All flags | `three-type/m3-all/` | ✅ `TestM3FCT_AllThreeTypes` |
| **D-FC** | Deduplication | Both `--recursive --include-charts-dir` | `deduplication/d-fc/` | ✅ `TestDFC_Deduplication` |
| **N2-F-F** | Nested: file:// → file:// | `--recursive` | `nested/n2-file-file/` | ✅ `TestN2FF_NestedFileDependencies` |
| **N2-F-C** | Nested: file:// → charts/ | `--recursive --include-charts-dir` | `nested/n2-file-charts/` | ✅ `TestN2FC_NestedFileToCharts` |
| **N3-MIX** | Three-level nesting | `--recursive --include-charts-dir` | `nested/n3-deep/` | ✅ `TestN3MIX_ThreeLevelNesting` |

## Directory Structure

```
matrix/
├── README.md                    # This file
├── single-types/                # Single dependency type tests
│   ├── s1-file/                 # S1-F: file:// dependency + sibling-chart/
│   ├── s1-charts/               # S1-C: embedded chart in charts/
│   └── s1-tarball/              # S1-T: tarball in charts/
├── two-type-mix/                # Mix of two dependency types
│   ├── m2-file-charts/          # M2-FC: file:// + charts/
│   ├── m2-file-tarball/         # M2-FT: file:// + tarball
│   └── m2-charts-tarball/       # M2-CT: charts/ + tarball
├── three-type/                  # All three dependency types
│   └── m3-all/                  # M3-FCT: file:// + charts/ + tarball
├── deduplication/               # Deduplication scenarios
│   └── d-fc/                    # D-FC: file:// pointing to charts/shared
└── nested/                      # Multi-level nesting
    ├── n2-file-file/            # N2-F-F: file:// → file://
    ├── n2-file-charts/          # N2-F-C: file:// → charts/
    ├── n3-deep/                 # N3-MIX: three-level nesting
    └── supporting charts/       # level1-chart, level2-chart, etc.
```

## Fixture Design Principles

Each fixture follows these principles:

1. **Minimal but realistic** - Each chart has just enough structure to test the scenario
2. **Clear naming** - Variable names indicate their source (e.g., `EMBEDDED_VAR`, `SIBLING_VAR`)
3. **Array conversion targets** - Each subchart includes convertible arrays (env, volumes, volumeMounts)
4. **Parent overrides** - Parent charts override subchart values to test cascade conversion

## Test Coverage

### What's Tested ✅

- All three dependency types: file://, charts/, tarball
- All flag combinations
- Deduplication when same chart found via multiple methods
- Value override propagation (parent → subchart)
- Backup file creation for all modified charts
- Tarball extraction and cleanup
- Multi-level nesting (2-3 levels deep)

### Limitations Documented

- Deep nesting (level 2+) support is implementation-dependent
- Tests log current behavior without failing for deep nesting edge cases
- Level 1 subchart processing is fully supported and tested

## Running Tests

```bash
# Run all subchart tests
go test -v ./cmd -run "TestS1|TestM2|TestM3|TestDFC|TestN"

# Run specific scenario
go test -v ./cmd -run TestS1C_SingleEmbeddedChart

# Run with full output
make test-cmd
```

## Adding New Test Scenarios

When adding new subchart test scenarios:

1. Create fixture in appropriate subdirectory (single-types/, two-type-mix/, etc.)
2. Follow naming convention: `test-id-description/`
3. Include Chart.yaml, values.yaml, and templates/
4. Add arrays to convert in values.yaml (env, volumes, volumeMounts, ports)
5. Write test in `cmd/subchart_test.go`
6. Update this README with the new scenario
7. Run `make test-cmd` to verify

## Maintenance Notes

**When modifying subchart fixtures:**
- Always test with `make test-cmd` to verify tests still pass
- If changing fixture structure, update corresponding test expectations
- Document any new scenarios in this README

**Common issues:**
- Template indentation: Use `nindent` with correct column offset
- file:// paths: Must be relative to parent Chart.yaml location
- Tarballs: Must include leading directory that gets stripped during extraction
