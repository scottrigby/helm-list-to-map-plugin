# Dependency Handling Plan

This document outlines the plan for handling different types of Helm chart dependencies in the `list-to-map` plugin.

## Current State (Phase 1 - Complete)

The plugin currently supports:

- **`--recursive` flag** for both `detect` and `convert` commands
- **`file://` dependencies** - Subcharts referenced via `file://` in `Chart.yaml` are converted at their source location
- **Umbrella values.yaml updates** - Automatically updates umbrella chart's values.yaml to match subchart format changes

### Usage

```bash
# Detect convertible paths in umbrella and all file:// subcharts
helm list-to-map detect --chart ./umbrella --recursive

# Convert umbrella and all file:// subcharts
helm list-to-map convert --chart ./umbrella --recursive
```

---

## Phase 2: Embedded Subcharts (`--include-charts-dir`)

### Problem

Some charts embed subcharts directly in their `charts/` directory without declaring them as dependencies in `Chart.yaml`. These are "vendored" subcharts.

Current behavior: These subcharts are NOT detected or converted.

### Proposed Solution

Add `--include-charts-dir` flag that:

1. Scans `charts/` subdirectory for any folders containing `Chart.yaml`
2. Converts those subcharts in-place
3. Updates umbrella values accordingly

### Usage

```bash
# Include subcharts in charts/ directory (regardless of Chart.yaml dependencies)
helm list-to-map convert --chart ./my-chart --include-charts-dir

# Can combine with --recursive for comprehensive conversion
helm list-to-map convert --chart ./umbrella --recursive --include-charts-dir
```

### Behavior Details

| Flag Combination                   | file:// deps | charts/ subcharts |
| ---------------------------------- | ------------ | ----------------- |
| (none)                             | No           | No                |
| `--recursive`                      | Yes          | No                |
| `--include-charts-dir`             | No           | Yes               |
| `--recursive --include-charts-dir` | Yes          | Yes               |

### Implementation Notes

1. Scan `charts/` for directories containing `Chart.yaml`
2. For each found subchart:
   - Run detection/conversion
   - Track converted paths
3. Update umbrella values for all converted subcharts
4. Handle potential overlap with `--recursive` (don't convert same chart twice)

---

## Phase 3: Remote Dependencies (`--expand-remote`)

### Problem

Dependencies from remote sources (Helm repos, OCI registries) are:

1. Pulled as tarballs into `charts/` by `helm dependency build`
2. The user may or may not own the source
3. Local modifications are overwritten on `helm dependency update`

### Scenarios

#### Scenario A: User Owns Remote Source

Example: Company has internal Helm repo with their own charts.

**Recommended workflow:**

1. Convert at source repository
2. Publish updated chart
3. Update umbrella to use new version

**Alternative (with `--expand-remote`):**

1. Expand tarball locally
2. Convert
3. WARNING: Changes will be lost on `helm dependency update`

#### Scenario B: Community Charts

Example: Using Bitnami MySQL chart.

**Recommended workflow:**

1. File upstream issue requesting map-based values support
2. Or fork the chart and convert to `file://` dependency

**Not recommended:** Converting locally (breaks on updates)

### Proposed Solution

Add `--expand-remote` flag that:

1. Extracts tarballs in `charts/` to directories
2. Converts those directories
3. Prints **prominent warning** about limitations

### Usage

```bash
# Expand and convert remote dependencies (with warnings)
helm list-to-map convert --chart ./umbrella --recursive --expand-remote
```

### Warning Output

```
┌─────────────────────────────────────────────────────────────────────────┐
│ WARNING: Converting remote dependencies                                  │
│                                                                          │
│ The following dependencies were expanded from tarballs and converted:   │
│   - mysql (https://charts.bitnami.com/bitnami)                          │
│   - redis (oci://registry.example.com/charts)                           │
│                                                                          │
│ These changes will be LOST if you run 'helm dependency update'.          │
│                                                                          │
│ Recommended actions:                                                     │
│   - If you own the source: convert the chart at its source repository   │
│   - If community chart: file an issue requesting map-based values       │
│   - Or: fork the chart and use file:// dependency                       │
└─────────────────────────────────────────────────────────────────────────┘
```

### Implementation Notes

1. Detect tarballs in `charts/` (`.tgz` files)
2. Extract to temporary directory or in-place
3. Run conversion
4. Track source repository for warning message
5. Consider generating a "migration guide" showing changes needed upstream

---

## Decision Matrix

| Dependency Type         | Default | With `--recursive` | With `--include-charts-dir` | With `--expand-remote` |
| ----------------------- | ------- | ------------------ | --------------------------- | ---------------------- |
| file:// in Chart.yaml   | Skip    | Convert at source  | Skip                        | Skip                   |
| Expanded dir in charts/ | Skip    | Skip               | Convert in-place            | Skip                   |
| Tarball in charts/      | Skip    | Skip               | Skip                        | Expand & convert       |
| Remote (not pulled)     | Skip    | Skip               | Skip                        | Skip (warn)            |

---

## Future Considerations

### Migration Guide Export

When converting with `--expand-remote`, could export a migration guide:

```bash
helm list-to-map convert --chart ./umbrella --expand-remote --export-migrations ./migrations/
```

Output:

```
migrations/
├── mysql-migration.md      # Changes to apply to upstream mysql chart
├── redis-migration.md      # Changes to apply to upstream redis chart
└── summary.md              # Overview of all changes
```

### Upstream PR Generation

Could potentially generate PRs for upstream charts:

```bash
helm list-to-map generate-upstream-pr --chart ./umbrella/charts/mysql
```

This is likely out of scope for this plugin but could be a separate tool.

---

## Implementation Priority

1. **Phase 1** (Complete): `--recursive` for `file://` dependencies
2. **Phase 2** (Next): `--include-charts-dir` for embedded subcharts
3. **Phase 3** (Later): `--expand-remote` for remote dependencies

Each phase is independently useful and can be released separately.
