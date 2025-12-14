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

**Status:** Not started

See [TESTING_PLAN.md](TESTING_PLAN.md) for the full testing strategy:

- Unit tests for `analyzer.go`, `crd.go`, `parser.go`
- Integration tests for CLI commands with isolated `HELM_CONFIG_HOME`
- Golden file tests for output regression testing
- Test fixtures: basic, nested-values, subcharts, dependencies, CRDs, partials, edge cases

### Add Makefile test targets

**Status:** Not started

```makefile
test:           # Run all tests
test-short:     # Skip network-dependent tests
test-coverage:  # Generate coverage report
test-update-golden:  # Update golden files
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

### Better subchart handling

**Status:** Not started

Improve detection of convertible fields in:

- Local subcharts (`charts/` directory)
- Remote dependencies (after `helm dependency build`)
- Inherited values from parent charts

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

### Verbose mode for detect

**Status:** ✅ Completed

Add `-v` flag to `detect` command showing:

- Detailed info for each convertible field (key, type, template, resource)
- Full reasons and suggestions for undetected fields
- Partial templates with their defines, values, and inclusion sources

### JSON/YAML output format

**Status:** Not started

Add `--output json` or `--output yaml` flags for machine-readable output, useful for CI/CD integration.

## Documentation

### Add examples directory

**Status:** Not started

Create `examples/` directory with:

- Before/after chart conversions
- Common patterns and their solutions
- Integration with popular charts (bitnami, prometheus-operator)
