# Roadmap

This document tracks planned enhancements for the list-to-map Helm plugin.

## Enhanced Detection Reporting

### Warn about non-detectable templates

**Status:** ✅ Completed

When `detect` finds `.Values` list usages in templates for unknown resource types (CRDs without loaded definitions), display a warning with actionable suggestions:

```
Potentially convertible (not auto-detected):
  customApp.listeners (in configmap.yaml:15)
    Reason: Custom Resource example.com/v1/MyResource without loaded CRD
    Suggestion: Load the CRD: helm list-to-map load-crd <crd-file>
    Or add manual rule: helm list-to-map add-rule --path='customApp.listeners[]' --uniqueKey=name
```

Use `-v` flag for verbose output with reasons and suggestions.

### Report partial templates

**Status:** ✅ Completed

Identify templates without `apiVersion`/`kind` (partials like `_helpers.tpl`, `_containers.tpl`) and report what they contain:

```
Partial templates:
  templates/_helpers.tpl
    Defines: chart.name, chart.fullname, chart.labels
    Values:  nameOverride, fullnameOverride, commonLabels
    Used by: deployment.yaml, service.yaml
```

Shown with `-v` flag.

### Improve CRD field coverage warnings

**Status:** ✅ Completed

When a CRD is loaded but a specific field path doesn't have `x-kubernetes-list-map-keys` defined in its OpenAPI schema, the detect command now shows:

```
  spec.volumes (in alertmanager.yaml:176)
    Reason: CRD field spec.volumes lacks x-kubernetes-list-map-keys
    Suggestion: helm list-to-map add-rule --path='spec.volumes[]' --uniqueKey=name
```

## Testing Infrastructure

### Implement comprehensive test suite

**Status:** ✅ Completed

See [TESTING_PLAN.md](TESTING_PLAN.md) for the full testing strategy:

- Unit tests for `pkg/k8s/`, `pkg/crd/`, `pkg/parser/`, `pkg/transform/`, `pkg/template/`
- Integration tests for CLI commands with isolated `HELM_CONFIG_HOME`
- Golden file tests for output regression testing
- Test fixtures: basic, nested-values, subcharts, dependencies, CRDs, partials, edge cases

### Add Makefile test targets

**Status:** ✅ Completed

```makefile
test:           # Run all tests
test-short:     # Skip network-dependent tests
test-cover:     # Generate coverage report
```

## Detection Improvements

### Embedded K8s type detection in CRDs

**Status:** ✅ Completed

CRDs that embed standard K8s types (Container, Volume, VolumeMount, etc.) are now detected automatically by analyzing the OpenAPI schema structure. This detects fields like:

- `spec.containers` (merge key: name)
- `spec.initContainers` (merge key: name)
- `spec.volumes` (merge key: name)
- `spec.volumeMounts` (merge key: mountPath)
- `spec.tolerations` (merge key: key)
- `spec.topologySpreadConstraints` (merge key: topologyKey)
- `spec.hostAliases` (merge key: ip)

### Nested list field warnings

**Status:** ✅ Completed

When detected fields like `containers` or `initContainers` are found, the CLI now shows a warning (in verbose mode) that these contain nested list fields (`env`, `volumeMounts`, `ports`) and suggests breaking them up for better override granularity.

### Umbrella Chart & Subchart Handling

**Status:** Phase 1 Complete, Phases 2-3 Planned

See [DEPENDENCY-HANDLING-PLAN.md](docs/DEPENDENCY-HANDLING-PLAN.md) for full details.

#### Phase 1: `--recursive` flag (Complete)

Support for umbrella charts with `file://` dependencies:

```bash
# Detect in umbrella and all file:// subcharts
helm list-to-map detect --chart ./umbrella --recursive

# Convert umbrella and all file:// subcharts
helm list-to-map convert --chart ./umbrella --recursive
```

Features:

- Parses `Chart.yaml` to find `file://` dependencies
- Converts subcharts at their source location
- Automatically updates umbrella `values.yaml` to match
- Supports template-only conversion (updates templates even when values.yaml has no entry)

**Planned Enhancement:** Global values support - When processing subcharts, check if fields like `imagePullSecrets` exist in umbrella's `global.*` values (which Helm merges into subcharts). This would allow detect to recognize that `imagePullSecrets` could come from `global.imagePullSecrets` in the parent chart.

#### Phase 2: `--include-charts-dir` flag (Planned)

Support for embedded subcharts in `charts/` directory (not declared in Chart.yaml):

```bash
helm list-to-map convert --chart ./my-chart --include-charts-dir
```

Use case: Charts that vendor subcharts directly without using dependency declarations.

#### Phase 3: `--expand-remote` flag (Planned)

Support for remote dependencies (Helm repos, OCI registries):

```bash
helm list-to-map convert --chart ./umbrella --recursive --expand-remote
```

This will:

- Extract tarballs in `charts/` to directories
- Convert those directories
- Print prominent warning about limitations

**Warning:** Changes to remote dependencies will be lost on `helm dependency update`. This is a last-resort option for users who need to convert but cannot modify the source.

Recommended workflow for remote dependencies:

1. Convert at source repository (if you own it)
2. File upstream issue (for community charts)
3. Fork and use `file://` dependency (if neither option works)

### Multi-document template support

**Status:** Not started

Handle templates with multiple YAML documents separated by `---`. Currently each document should be parsed independently to extract its own `apiVersion`/`kind`.

### Templated apiVersion/kind support

**Status:** Not started

Some charts use templated `apiVersion` or `kind`:

```yaml
apiVersion: { { .Values.apiVersion | default "apps/v1" } }
kind: { { .Values.kind | default "Deployment" } }
```

These are currently skipped. Could potentially:

- Use default values from `values.yaml`
- Allow user hints via config

## CLI Improvements

### Multiple values files support (`-f`/`--values`)

**Status:** Not started

Support custom values files in addition to the chart's default `values.yaml`, mirroring Helm's `-f` flag behavior:

```bash
# Detect with additional values files
helm list-to-map detect --chart ./my-chart -f values-dev.yaml -f values-prod.yaml

# Convert with additional values files
helm list-to-map convert --chart ./my-chart -f values-dev.yaml -f values-prod.yaml
```

**Use cases:**

1. **Environment-specific overrides** - Teams often maintain `values-dev.yaml`, `values-staging.yaml`, `values-prod.yaml` that override the base `values.yaml`. These override files may contain arrays that need conversion.

2. **CI/CD pipelines** - Pipelines may pass `-f` flags to customize deployments. The converted override files need to match the converted base chart.

3. **Local development** - Developers use local values files that override chart defaults.

**Implementation requirements:**

- Accept `-f`/`--values` flags (repeatable, like Helm)
- Process each values file for array conversion
- Maintain file order (later files override earlier ones, matching Helm semantics)
- Report which file each detected candidate came from
- Handle paths relative to CWD or chart directory
- Consider: Should converted override files match the base chart's new map structure?

**Edge cases to consider:**

- Override file has array that base chart doesn't have
- Override file has map where base chart has array (conflict)
- Override file only overrides specific array items (partial override)
- Values file doesn't exist (error handling)

### Verbose mode for detect

**Status:** ✅ Completed

Add `-v` flag to `detect` command showing:

- Detailed info for each convertible field (key, type, template, resource)
- Full reasons and suggestions for undetected fields
- Partial templates with their defines, values, and inclusion sources

### JSON/YAML output format

**Status:** Not started

Add `--output json` or `--output yaml` flags for machine-readable output, useful for CI/CD integration.

## Testing & Validation

### Test against community charts

**Status:** In progress

Test template detection and rewriting patterns against popular community Helm charts to discover real-world template patterns.

**Charts:**

- [x] [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) - Successfully converted CRD fields (tolerations, containers, volumes, etc.)
- [ ] [ingress-nginx](https://github.com/kubernetes/ingress-nginx/tree/main/charts/ingress-nginx)
- [ ] [cert-manager](https://github.com/cert-manager/cert-manager/tree/master/deploy/charts/cert-manager)
- [ ] [argo-cd](https://github.com/argoproj/argo-helm/tree/main/charts/argo-cd)
- [ ] [grafana](https://github.com/grafana/helm-charts/tree/main/charts/grafana)
- [ ] [external-secrets](https://github.com/external-secrets/external-secrets/tree/main/deploy/charts/external-secrets)

**Goals:**

- Identify common template patterns not yet supported
- Build a pattern catalog with real examples
- Ensure regex patterns handle whitespace/formatting variations
- Document edge cases and limitations

**Supported patterns:**

See [Template Rewriting Patterns](ARCHITECTURE.md#template-rewriting-patterns) in ARCHITECTURE.md for the current list of supported patterns. When testing reveals new patterns in community charts:

1. Update `ReplaceListBlocks()` in `pkg/template/rewrite.go` with new regex patterns
2. Add pattern examples to the ARCHITECTURE.md section
3. Note which chart(s) use the pattern

## Documentation

### Add examples directory

**Status:** Not started

Create `examples/` directory with:

- Before/after chart conversions
- Common patterns and their solutions
- Integration with popular charts (bitnami, prometheus-operator)
