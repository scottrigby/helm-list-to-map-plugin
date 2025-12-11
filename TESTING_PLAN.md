# Testing Plan for list-to-map Helm Plugin

This document outlines the testing strategy for the list-to-map Helm plugin using idiomatic Go testing patterns.

## Overview

The testing strategy covers:

1. Unit tests for individual functions
2. Integration tests for CLI commands
3. Golden file tests for output verification
4. Test fixtures (charts) for realistic scenarios

## Proposed CLI Enhancements

Before implementing tests, we should enhance the `detect` command output to provide better visibility into:

### 1. Non-Detectable Templates Warning

Templates that contain `.Values` list usages but cannot be auto-detected should be reported:

```
Detected convertible arrays:
· env → key=name, type=corev1.EnvVar
· volumes → key=name, type=corev1.Volume

⚠ Potentially convertible (not auto-detected):
· customApp.listeners (in templates/configmap.yaml:15)
    Reason: Unknown resource type (Custom Resource without loaded CRD)
    Suggestion: Load the CRD with 'helm list-to-map load-crd <crd-file>'
                or add a manual rule with 'helm list-to-map add-rule --path=customApp.listeners[] --uniqueKey=port'

· istio.virtualService.http (in templates/virtualservice.yaml:23)
    Reason: CRD loaded but field 'spec.http' has no x-kubernetes-list-map-keys
    Suggestion: Add manual rule: helm list-to-map add-rule --path='istio.virtualService.http[]' --uniqueKey=name
```

### 2. Partial Template Detection

Templates without `apiVersion` and `kind` (partials/includes) should be identified:

```
Partial templates (included in other templates):
· templates/_containers.tpl
    Defines: chart.containers, chart.initContainers
    Contains .Values usages: extraContainers, initContainers

· templates/_volumes.tpl
    Defines: chart.volumes
    Contains .Values usages: extraVolumes, persistentVolumes

Note: Partial templates are analyzed when included from resource templates.
      List fields in partials are detected via their inclusion context.
```

### 3. Implementation Changes Needed

**In `cmd/analyzer.go`:**

```go
// UndetectedCandidate represents a .Values list usage that couldn't be auto-detected
type UndetectedCandidate struct {
    ValuesPath   string
    TemplateFile string
    LineNumber   int
    Reason       string // "unknown_resource", "crd_missing_keys", "partial_template"
    Suggestion   string
}

// PartialTemplate represents a template without apiVersion/kind
type PartialTemplate struct {
    FilePath     string
    DefinedNames []string   // Template names defined (e.g., "chart.volumes")
    ValuesUsages []string   // .Values paths used
}

// DetectionResult combines all detection outputs
type DetectionResult struct {
    Candidates  []DetectedCandidate
    Undetected  []UndetectedCandidate
    Partials    []PartialTemplate
}
```

**In `cmd/parser.go`:**

```go
// Add to ParsedTemplate struct
type ParsedTemplate struct {
    FilePath    string
    APIVersion  string
    Kind        string
    GoType      reflect.Type
    Directives  []TemplateDirective
    IsPartial   bool            // true if no apiVersion/kind
    DefinedNames []string       // template names defined via {{- define "..." }}
}
```

## Directory Structure

```
cmd/
├── analyzer.go
├── analyzer_test.go      # Unit tests for K8s type introspection
├── crd.go
├── crd_test.go           # Unit tests for CRD parsing
├── parser.go
├── parser_test.go        # Unit tests for template parsing
├── main.go
├── main_test.go          # Integration tests for CLI commands
└── testdata/
    ├── charts/
    │   ├── basic/                    # Simple chart with common K8s types
    │   │   ├── Chart.yaml
    │   │   ├── values.yaml
    │   │   └── templates/
    │   │       ├── deployment.yaml
    │   │       └── _helpers.tpl
    │   ├── nested-values/            # Deeply nested values structure
    │   │   ├── Chart.yaml
    │   │   ├── values.yaml
    │   │   └── templates/
    │   │       └── deployment.yaml
    │   ├── with-subchart/            # Chart with subcharts (local)
    │   │   ├── Chart.yaml
    │   │   ├── values.yaml
    │   │   ├── templates/
    │   │   │   └── deployment.yaml
    │   │   └── charts/
    │   │       └── subchart/
    │   │           ├── Chart.yaml
    │   │           ├── values.yaml
    │   │           └── templates/
    │   │               └── deployment.yaml
    │   ├── with-dependency/          # Chart with remote dependency
    │   │   ├── Chart.yaml            # (requires helm dependency build)
    │   │   ├── Chart.lock
    │   │   ├── values.yaml
    │   │   └── templates/
    │   │       └── deployment.yaml
    │   ├── crd-based/                # Chart using Custom Resources
    │   │   ├── Chart.yaml
    │   │   ├── values.yaml
    │   │   └── templates/
    │   │       ├── prometheusrule.yaml
    │   │       └── alertmanager.yaml
    │   ├── multiple-resources/       # Multiple K8s resources in one file
    │   │   ├── Chart.yaml
    │   │   ├── values.yaml
    │   │   └── templates/
    │   │       └── all.yaml
    │   ├── edge-cases/               # Edge cases and unusual patterns
    │   │   ├── Chart.yaml
    │   │   ├── values.yaml
    │   │   └── templates/
    │   │       ├── empty-arrays.yaml
    │   │       ├── inline-templates.yaml
    │   │       └── range-patterns.yaml
    │   ├── with-partials/            # Chart with partial templates
    │   │   ├── Chart.yaml
    │   │   ├── values.yaml
    │   │   └── templates/
    │   │       ├── deployment.yaml
    │   │       ├── _helpers.tpl
    │   │       ├── _containers.tpl   # Partial with container-related lists
    │   │       └── _volumes.tpl      # Partial with volume-related lists
    │   ├── undetectable-crds/        # Chart with CRs that can't be auto-detected
    │   │   ├── Chart.yaml
    │   │   ├── values.yaml
    │   │   └── templates/
    │   │       ├── virtualservice.yaml  # Istio VirtualService (no CRD loaded)
    │   │       └── custom-resource.yaml # Unknown custom resource
    │   └── already-converted/        # Chart already using map pattern
    │       ├── Chart.yaml
    │       ├── values.yaml
    │       └── templates/
    │           └── deployment.yaml
    ├── crds/
    │   ├── prometheus-crd.yaml       # PrometheusRule CRD
    │   ├── alertmanager-crd.yaml     # Alertmanager CRD
    │   └── multi-doc.yaml            # Multi-document CRD file
    └── golden/
        ├── detect/
        │   ├── basic.txt
        │   ├── nested-values.txt
        │   ├── crd-based.txt
        │   └── already-converted.txt
        ├── convert/
        │   ├── basic/
        │   │   ├── values.yaml
        │   │   └── templates/
        │   │       ├── deployment.yaml
        │   │       └── _helpers.tpl
        │   └── nested-values/
        │       ├── values.yaml
        │       └── templates/
        │           └── deployment.yaml
        └── rules/
            └── with-custom-rules.txt
```

## Test Categories

### 1. Unit Tests

#### `analyzer_test.go` - K8s Type Introspection

```go
func TestResolveKubeAPIType(t *testing.T)
// Test cases:
// - Core types (v1/Pod, v1/Service)
// - Apps types (apps/v1/Deployment)
// - Networking types (networking.k8s.io/v1/Ingress)
// - Unknown types return nil

func TestNavigateFieldSchema(t *testing.T)
// Test cases:
// - Simple path (spec.replicas)
// - Nested path (spec.template.spec.containers)
// - Path to slice (spec.template.spec.volumes)
// - Path with patchMergeKey detection
// - Invalid paths return error
// - Embedded/inline struct handling

func TestIsConvertibleField(t *testing.T)
// Test cases:
// - Convertible fields (containers, volumes, env)
// - Non-convertible slices (no patchMergeKey)
// - Non-slice fields
// - Verify correct patchMergeKey values (volumeMounts→mountPath, ports→containerPort)
```

#### `crd_test.go` - CRD Schema Parsing

```go
func TestCRDRegistry_LoadFromFile(t *testing.T)
// Test cases:
// - Single CRD document
// - Multi-document YAML
// - Non-CRD documents are skipped
// - Invalid YAML returns error

func TestCRDRegistry_LoadFromDirectory(t *testing.T)
// Test cases:
// - Directory with multiple CRD files
// - Mixed CRD and non-CRD files
// - Empty directory

func TestCRDRegistry_GetFieldInfo(t *testing.T)
// Test cases:
// - Known type and path returns info
// - Unknown type returns nil
// - Unknown path returns nil
// - Verify MapKeys extraction

func TestFindCRDListFields(t *testing.T)
// Test cases:
// - Deeply nested properties
// - Arrays of objects with properties
// - Multiple map-type lists in one CRD
```

#### `parser_test.go` - Template Parsing

```go
func TestExtractAPIVersionAndKind(t *testing.T)
// Test cases:
// - Explicit values
// - Templated values (skip)
// - Missing apiVersion or kind

func TestIsPartialTemplate(t *testing.T)
// Test cases:
// - File with apiVersion and kind → false
// - File without apiVersion/kind but has {{- define }} → true
// - _helpers.tpl style files → true
// - Regular YAML without define → depends on context

func TestExtractDefinedTemplateNames(t *testing.T)
// Test cases:
// - Single define block
// - Multiple define blocks
// - Nested define (rare but valid)
// - No define blocks

func TestExtractDirectives(t *testing.T)
// Test cases:
// - toYaml .Values.X
// - with .Values.X ... end
// - toYaml . inside with block
// - range patterns
// - Nested indentation tracking
// - Multiple directives per file

func TestAnalyzeDirectiveContent(t *testing.T)
// Test cases:
// - toYaml pattern
// - toYaml_dot pattern (inside with)
// - range pattern
// - range_kv pattern (already map-like)
// - with pattern

func TestFollowIncludeChain(t *testing.T)
// Test cases:
// - Direct include
// - Nested includes
// - Circular include prevention
// - Include with .Values usage inside
```

### 2. Integration Tests

#### `main_test.go` - CLI Command Tests

```go
// Test helper for setting up isolated test environment
func setupTestEnv(t *testing.T) (chartDir, configDir string, cleanup func()) {
    // Create temp directories
    // Set HELM_CONFIG_HOME env var
    // Return cleanup function
}

func TestDetectCommand(t *testing.T)
// Test cases using table-driven tests:
// - Basic chart detection
// - Nested values detection
// - CRD-based detection (with loaded CRDs)
// - Already converted chart (no candidates)
// - Chart with manual rules
// - Verify output format

func TestConvertCommand(t *testing.T)
// Test cases:
// - Basic conversion (verify values.yaml and templates modified)
// - Dry-run mode (no files modified)
// - Backup file creation
// - Helper template generation
// - Idempotent (running twice produces same result)

func TestLoadCRDCommand(t *testing.T)
// Test cases:
// - Load from local file
// - Load from URL (use httptest server)
// - Invalid file path
// - Invalid URL
// - Verify CRDs stored in config directory

func TestListCRDsCommand(t *testing.T)
// Test cases:
// - No CRDs loaded
// - CRDs loaded, normal output
// - CRDs loaded, verbose output

func TestAddRuleCommand(t *testing.T)
// Test cases:
// - Add new rule
// - Duplicate rule (update or error?)
// - Invalid path format
// - Verify config.yaml updated

func TestRulesCommand(t *testing.T)
// Test cases:
// - No custom rules
// - With custom rules
// - Rules affect detect output
```

### 3. Golden File Tests

Golden files allow regression testing by comparing output against known-good snapshots.

```go
var updateGolden = flag.Bool("update-golden", false, "update golden files")

func TestDetectGolden(t *testing.T) {
    tests := []struct {
        name      string
        chartDir  string
        crdFiles  []string
        goldenFile string
    }{
        {"basic", "testdata/charts/basic", nil, "testdata/golden/detect/basic.txt"},
        {"nested-values", "testdata/charts/nested-values", nil, "testdata/golden/detect/nested-values.txt"},
        {"crd-based", "testdata/charts/crd-based", []string{"testdata/crds/prometheus-crd.yaml"}, "testdata/golden/detect/crd-based.txt"},
        {"with-partials", "testdata/charts/with-partials", nil, "testdata/golden/detect/with-partials.txt"},
        {"undetectable", "testdata/charts/undetectable-crds", nil, "testdata/golden/detect/undetectable.txt"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Run detect command
            // Compare output to golden file (includes warnings and partial info)
            // If -update-golden flag, write new golden file
        })
    }
}

func TestConvertGolden(t *testing.T) {
    // Similar pattern, but compare directory trees
}
```

### 4. Test Fixtures

#### Basic Chart (`testdata/charts/basic/`)

**values.yaml:**

```yaml
replicas: 1
image:
  repository: nginx
  tag: latest

env:
  - name: DB_HOST
    value: localhost
  - name: DB_PORT
    value: "5432"

volumes:
  - name: config
    configMap:
      name: my-config
  - name: data
    emptyDir: {}

volumeMounts:
  - name: config
    mountPath: /etc/config
  - name: data
    mountPath: /data
```

**templates/deployment.yaml:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: { { .Release.Name } }
spec:
  replicas: { { .Values.replicas } }
  template:
    spec:
      containers:
        - name: app
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          env: { { - toYaml .Values.env | nindent 12 } }
          volumeMounts: { { - toYaml .Values.volumeMounts | nindent 12 } }
      volumes: { { - toYaml .Values.volumes | nindent 8 } }
```

#### Nested Values Chart (`testdata/charts/nested-values/`)

**values.yaml:**

```yaml
app:
  primary:
    deployment:
      containers:
        main:
          env:
            - name: LOG_LEVEL
              value: info
          ports:
            - containerPort: 8080
              name: http
            - containerPort: 9090
              name: metrics
```

#### CRD-Based Chart (`testdata/charts/crd-based/`)

Uses PrometheusRule with `spec.groups[].rules[]`.

#### Edge Cases Chart (`testdata/charts/edge-cases/`)

- Empty arrays: `volumes: []`
- Inline templates on same line as key
- Range patterns that shouldn't be converted
- Already-map patterns

## Test Helpers

```go
// copyDir recursively copies a directory for isolated testing
func copyDir(t *testing.T, src, dst string)

// diffDir compares two directories and reports differences
func diffDir(t *testing.T, expected, actual string) []string

// runCommand executes the CLI and captures output
func runCommand(t *testing.T, args ...string) (stdout, stderr string, exitCode int)

// assertFileContains checks file content
func assertFileContains(t *testing.T, path, substring string)

// assertFileEquals compares file to expected content
func assertFileEquals(t *testing.T, path, expectedContent string)

// loadGolden loads or updates a golden file
func loadGolden(t *testing.T, name string, actual []byte) []byte
```

## Environment Setup

### Required Environment Variables for Tests

```go
// setupTestEnv creates isolated test environment
func setupTestEnv(t *testing.T) (cleanup func()) {
    t.Helper()

    // Create temp directory for HELM_CONFIG_HOME
    configDir := t.TempDir()
    t.Setenv("HELM_CONFIG_HOME", configDir)

    // Create list-to-map subdirectories
    os.MkdirAll(filepath.Join(configDir, "list-to-map", "crds"), 0755)

    return func() {
        // Cleanup is automatic with t.TempDir()
    }
}
```

### Handling `helm dependency build`

For charts with remote dependencies:

```go
func TestWithDependency(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping test requiring network in short mode")
    }

    chartDir := copyDir(t, "testdata/charts/with-dependency", t.TempDir())

    // Run helm dependency build
    cmd := exec.Command("helm", "dependency", "build", chartDir)
    if err := cmd.Run(); err != nil {
        t.Fatalf("helm dependency build failed: %v", err)
    }

    // Now run detect/convert tests
}
```

## Makefile Targets

Add to `Makefile`:

```makefile
.PHONY: test
test:
	go test -v ./cmd/...

.PHONY: test-short
test-short:
	go test -v -short ./cmd/...

.PHONY: test-update-golden
test-update-golden:
	go test -v ./cmd/... -update-golden

.PHONY: test-coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./cmd/...
	go tool cover -html=coverage.out -o coverage.html
```

## Implementation Order

1. **Phase 1: Test Infrastructure**
   - Create `testdata/` directory structure
   - Implement test helpers (copyDir, diffDir, etc.)
   - Create basic test chart fixture

2. **Phase 2: Unit Tests**
   - `analyzer_test.go` - Test K8s type introspection
   - `parser_test.go` - Test template parsing
   - `crd_test.go` - Test CRD parsing

3. **Phase 3: Integration Tests**
   - `main_test.go` - Test CLI commands
   - Golden file infrastructure
   - Environment isolation (HELM_CONFIG_HOME)

4. **Phase 4: Extended Fixtures**
   - Add more chart fixtures for edge cases
   - CRD test fixtures
   - Golden files for all fixtures

5. **Phase 5: CI Integration**
   - Add test targets to Makefile
   - Configure GitHub Actions workflow

## Notes

- All tests should be parallelizable where possible (`t.Parallel()`)
- Use `t.TempDir()` for automatic cleanup
- Use `t.Setenv()` for environment variable isolation (Go 1.17+)
- Tests requiring network should be skipped in `-short` mode
- Golden file updates require explicit `-update-golden` flag
