# Roadmap

This document tracks planned enhancements for the list-to-map Helm plugin.

## Enhanced Detection Reporting

### Warn about non-detectable templates

**Status:** Not started

When `detect` finds `.Values` list usages in templates for unknown resource types (CRDs without loaded definitions), display a warning with actionable suggestions:

```
⚠ Potentially convertible (not auto-detected):
· customApp.listeners (in templates/configmap.yaml:15)
    Reason: Unknown resource type (Custom Resource without loaded CRD)
    Suggestion: Load the CRD with 'helm list-to-map load-crd <crd-file>'
                or add a manual rule with 'helm list-to-map add-rule --path=customApp.listeners[] --uniqueKey=port'
```

Implementation:

- Track all `.Values` list usages found during template scanning
- Compare against successfully detected candidates
- Report the difference with file/line info and suggested actions

### Report partial templates

**Status:** Not started

Identify templates without `apiVersion`/`kind` (partials like `_helpers.tpl`, `_containers.tpl`) and report what they contain:

```
Partial templates (included in other templates):
· templates/_containers.tpl
    Defines: chart.containers, chart.initContainers
    Contains .Values usages: extraContainers, initContainers
```

Implementation:

- Detect files with `{{- define "..." }}` but no `apiVersion`/`kind`
- Extract defined template names and `.Values` usages
- Note that partials are analyzed via their inclusion context

### Improve CRD field coverage warnings

**Status:** Not started

When a CRD is loaded but a specific field path doesn't have `x-kubernetes-list-map-keys` defined in its OpenAPI schema, suggest adding a manual rule.

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

**Status:** Not started

Add `-v` flag to `detect` command showing:

- Which templates were scanned
- Which resource types were resolved
- Full YAML paths for each candidate

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
