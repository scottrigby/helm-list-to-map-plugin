# Testing Plan for list-to-map Helm Plugin

This document provides a concrete implementation plan for the list-to-map test suite using idiomatic Go testing patterns.

## Current State

- **Source files**: `cmd/analyzer.go`, `cmd/crd.go`, `cmd/parser.go`, `cmd/main.go`
- **Test files** (74 test functions total):
  - `cmd/testutil_test.go` - Test utilities (setupTestEnv, copyChart, getTestdataPath)
  - `cmd/glob_test.go` - Tests for matchGlob, matchRule (100% coverage)
  - `cmd/transform_test.go` - Tests for array transformation (100% coverage)
  - `cmd/template_test.go` - Tests for template rewriting (core: 100%, replaceListBlocks: 76%)
  - `cmd/integration_test.go` - Integration tests for chart loading, detection
  - `cmd/cli_test.go` - CLI tests (detect, convert, --recursive, help)
  - `cmd/crd_test.go` - CRD registry, loading, parsing, embedded K8s type detection
  - `cmd/error_test.go` - Error handling, edge cases, CLI error scenarios
  - `cmd/golden_test.go` - Golden file tests for detect output verification
- **Test fixtures**:
  - `cmd/testdata/charts/basic/` - Standard env, volumes, volumeMounts
  - `cmd/testdata/charts/nested-values/` - Nested paths like `app.primary.env`
  - `cmd/testdata/charts/edge-cases/` - Empty arrays, duplicates, block sequences
  - `cmd/testdata/charts/all-patterns/` - All 5 template patterns
  - `cmd/testdata/charts/umbrella/` - Umbrella chart with file:// subchart for --recursive
- **Golden files**:
  - `cmd/testdata/golden/detect/` - Expected detect output for basic, nested-values, all-patterns charts

## Implementation Phases

### Phase 1: Test Infrastructure

Create the directory structure and test helpers.

#### 1.1 Create Directory Structure

```
cmd/
├── testdata/
│   ├── charts/
│   │   ├── basic/
│   │   ├── nested-values/
│   │   ├── crd-based/
│   │   ├── with-partials/
│   │   ├── multiple-resources/
│   │   └── edge-cases/
│   ├── crds/
│   └── golden/
│       ├── detect/
│       └── convert/
└── testutil_test.go
```

#### 1.2 Test Utilities (`cmd/testutil_test.go`)

```go
package main

import (
    "bytes"
    "os"
    "path/filepath"
    "testing"
)

// setupTestEnv creates an isolated HELM_CONFIG_HOME for tests
func setupTestEnv(t *testing.T) string {
    t.Helper()
    configDir := t.TempDir()
    t.Setenv("HELM_CONFIG_HOME", configDir)

    // Create plugin config structure
    pluginDir := filepath.Join(configDir, "list-to-map")
    if err := os.MkdirAll(filepath.Join(pluginDir, "crds"), 0755); err != nil {
        t.Fatalf("failed to create config dir: %v", err)
    }
    return pluginDir
}

// copyChart copies a test chart to a temp directory for modification
func copyChart(t *testing.T, srcChart string) string {
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

// goldenFile loads or updates a golden file
func goldenFile(t *testing.T, name string, actual []byte, update bool) []byte {
    t.Helper()
    goldenPath := filepath.Join("testdata", "golden", name)

    if update {
        if err := os.MkdirAll(filepath.Dir(goldenPath), 0755); err != nil {
            t.Fatalf("failed to create golden dir: %v", err)
        }
        if err := os.WriteFile(goldenPath, actual, 0644); err != nil {
            t.Fatalf("failed to write golden file: %v", err)
        }
        return actual
    }

    expected, err := os.ReadFile(goldenPath)
    if err != nil {
        t.Fatalf("failed to read golden file %s: %v", goldenPath, err)
    }
    return expected
}

// assertEqualStrings compares strings with diff output on failure
func assertEqualStrings(t *testing.T, expected, actual, context string) {
    t.Helper()
    if expected != actual {
        t.Errorf("%s mismatch:\nExpected:\n%s\n\nActual:\n%s", context, expected, actual)
    }
}
```

### Phase 2: Unit Tests

#### 2.1 Analyzer Tests (`cmd/analyzer_test.go`)

```go
package main

import (
    "reflect"
    "testing"

    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
)

func TestResolveKubeAPIType(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name       string
        apiVersion string
        kind       string
        wantNil    bool
        wantType   reflect.Type
    }{
        {
            name:       "core Pod",
            apiVersion: "v1",
            kind:       "Pod",
            wantType:   reflect.TypeOf(corev1.Pod{}),
        },
        {
            name:       "apps Deployment",
            apiVersion: "apps/v1",
            kind:       "Deployment",
            wantType:   reflect.TypeOf(appsv1.Deployment{}),
        },
        {
            name:       "unknown CRD",
            apiVersion: "custom.io/v1",
            kind:       "MyResource",
            wantNil:    true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := resolveKubeAPIType(tt.apiVersion, tt.kind)
            if tt.wantNil {
                if got != nil {
                    t.Errorf("expected nil, got %v", got)
                }
                return
            }
            if got != tt.wantType {
                t.Errorf("expected %v, got %v", tt.wantType, got)
            }
        })
    }
}

func TestNavigateFieldSchema(t *testing.T) {
    t.Parallel()

    deployType := reflect.TypeOf(appsv1.Deployment{})

    tests := []struct {
        name       string
        rootType   reflect.Type
        yamlPath   string
        wantErr    bool
        wantSlice  bool
        wantKey    string
    }{
        {
            name:      "spec.replicas (non-slice)",
            rootType:  deployType,
            yamlPath:  "spec.replicas",
            wantSlice: false,
        },
        {
            name:      "spec.template.spec.containers",
            rootType:  deployType,
            yamlPath:  "spec.template.spec.containers",
            wantSlice: true,
            wantKey:   "name",
        },
        {
            name:      "spec.template.spec.volumes",
            rootType:  deployType,
            yamlPath:  "spec.template.spec.volumes",
            wantSlice: true,
            wantKey:   "name",
        },
        {
            name:      "volumeMounts uses mountPath not name",
            rootType:  reflect.TypeOf(corev1.Container{}),
            yamlPath:  "volumeMounts",
            wantSlice: true,
            wantKey:   "mountPath",
        },
        {
            name:      "ports uses containerPort not name",
            rootType:  reflect.TypeOf(corev1.Container{}),
            yamlPath:  "ports",
            wantSlice: true,
            wantKey:   "containerPort",
        },
        {
            name:     "invalid path",
            rootType: deployType,
            yamlPath: "spec.nonexistent.field",
            wantErr:  true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            info, err := navigateFieldSchema(tt.rootType, tt.yamlPath)

            if tt.wantErr {
                if err == nil {
                    t.Errorf("expected error, got nil")
                }
                return
            }
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }

            if info.IsSlice != tt.wantSlice {
                t.Errorf("IsSlice: expected %v, got %v", tt.wantSlice, info.IsSlice)
            }
            if tt.wantKey != "" && info.MergeKey != tt.wantKey {
                t.Errorf("MergeKey: expected %q, got %q", tt.wantKey, info.MergeKey)
            }
        })
    }
}

func TestIsConvertibleField(t *testing.T) {
    t.Parallel()

    deployType := reflect.TypeOf(appsv1.Deployment{})

    tests := []struct {
        name         string
        rootType     reflect.Type
        yamlPath     string
        wantConvert  bool
        wantMergeKey string
    }{
        {
            name:         "containers is convertible",
            rootType:     deployType,
            yamlPath:     "spec.template.spec.containers",
            wantConvert:  true,
            wantMergeKey: "name",
        },
        {
            name:         "volumes is convertible",
            rootType:     deployType,
            yamlPath:     "spec.template.spec.volumes",
            wantConvert:  true,
            wantMergeKey: "name",
        },
        {
            name:        "replicas is not convertible (not a slice)",
            rootType:    deployType,
            yamlPath:    "spec.replicas",
            wantConvert: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            info := isConvertibleField(tt.rootType, tt.yamlPath)

            if tt.wantConvert {
                if info == nil {
                    t.Errorf("expected convertible field, got nil")
                    return
                }
                if info.MergeKey != tt.wantMergeKey {
                    t.Errorf("MergeKey: expected %q, got %q", tt.wantMergeKey, info.MergeKey)
                }
            } else {
                if info != nil {
                    t.Errorf("expected nil (not convertible), got %+v", info)
                }
            }
        })
    }
}
```

#### 2.2 CRD Tests (`cmd/crd_test.go`)

```go
package main

import (
    "os"
    "path/filepath"
    "testing"
)

func TestCRDRegistry_LoadFromFile(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name      string
        content   string
        wantTypes int
        wantErr   bool
    }{
        {
            name: "valid CRD with list-map-keys",
            content: `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: tests.example.com
spec:
  group: example.com
  names:
    kind: Test
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                items:
                  type: array
                  x-kubernetes-list-type: map
                  x-kubernetes-list-map-keys:
                    - name
                  items:
                    type: object
                    properties:
                      name:
                        type: string
`,
            wantTypes: 1,
        },
        {
            name: "non-CRD document skipped",
            content: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`,
            wantTypes: 0,
        },
        {
            name:    "invalid YAML",
            content: "not: valid: yaml: {{",
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Write temp file
            tmpFile := filepath.Join(t.TempDir(), "crd.yaml")
            if err := os.WriteFile(tmpFile, []byte(tt.content), 0644); err != nil {
                t.Fatalf("failed to write temp file: %v", err)
            }

            reg := NewCRDRegistry()
            err := reg.LoadFromFile(tmpFile)

            if tt.wantErr {
                if err == nil {
                    t.Errorf("expected error, got nil")
                }
                return
            }
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }

            types := reg.ListTypes()
            if len(types) != tt.wantTypes {
                t.Errorf("expected %d types, got %d: %v", tt.wantTypes, len(types), types)
            }
        })
    }
}

func TestCRDRegistry_LoadFromDirectory(t *testing.T) {
    t.Parallel()

    // Create temp directory with multiple CRD files
    tmpDir := t.TempDir()

    crd1 := `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ones.example.com
spec:
  group: example.com
  names:
    kind: One
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
`
    crd2 := `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: twos.example.com
spec:
  group: example.com
  names:
    kind: Two
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
`

    os.WriteFile(filepath.Join(tmpDir, "crd-one.yaml"), []byte(crd1), 0644)
    os.WriteFile(filepath.Join(tmpDir, "crd-two.yaml"), []byte(crd2), 0644)
    os.WriteFile(filepath.Join(tmpDir, "not-a-crd.yaml"), []byte("foo: bar"), 0644)

    reg := NewCRDRegistry()
    err := reg.LoadFromDirectory(tmpDir)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    // Should have loaded 2 CRD types (skipped not-a-crd.yaml because no "crd" in name)
    types := reg.ListTypes()
    if len(types) != 2 {
        t.Errorf("expected 2 types, got %d: %v", len(types), types)
    }
}

func TestDetectEmbeddedK8sType(t *testing.T) {
    t.Parallel()
    // Test that Container-like schema is detected
    // This requires setting up a yaml.Node structure
    // Implementation depends on internal function signature
}
```

#### 2.3 Parser Tests (`cmd/parser_test.go`)

```go
package main

import (
    "testing"
)

func TestExtractAPIVersionAndKind(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name           string
        content        string
        wantAPIVersion string
        wantKind       string
    }{
        {
            name: "explicit values",
            content: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test
`,
            wantAPIVersion: "apps/v1",
            wantKind:       "Deployment",
        },
        {
            name: "templated apiVersion",
            content: `
apiVersion: {{ .Values.apiVersion }}
kind: Deployment
`,
            wantAPIVersion: "", // Cannot resolve templated values
            wantKind:       "Deployment",
        },
        {
            name: "missing kind",
            content: `
apiVersion: v1
metadata:
  name: test
`,
            wantAPIVersion: "v1",
            wantKind:       "",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            apiVersion, kind := extractAPIVersionAndKind(tt.content)
            if apiVersion != tt.wantAPIVersion {
                t.Errorf("apiVersion: expected %q, got %q", tt.wantAPIVersion, apiVersion)
            }
            if kind != tt.wantKind {
                t.Errorf("kind: expected %q, got %q", tt.wantKind, kind)
            }
        })
    }
}

func TestExtractDefinedTemplateNames(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name    string
        content string
        want    []string
    }{
        {
            name: "single define",
            content: `
{{- define "mychart.labels" -}}
app: test
{{- end }}
`,
            want: []string{"mychart.labels"},
        },
        {
            name: "multiple defines",
            content: `
{{- define "mychart.labels" -}}
{{- end }}
{{- define "mychart.selectorLabels" -}}
{{- end }}
`,
            want: []string{"mychart.labels", "mychart.selectorLabels"},
        },
        {
            name:    "no defines",
            content: "just: yaml",
            want:    nil,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := extractDefinedTemplateNames(tt.content)
            if len(got) != len(tt.want) {
                t.Errorf("expected %d names, got %d: %v", len(tt.want), len(got), got)
            }
        })
    }
}

func TestAnalyzeDirectiveContent(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name        string
        content     string
        withContext string
        wantPattern string
        wantPath    string
    }{
        {
            name:        "toYaml pattern",
            content:     "{{- toYaml .Values.env | nindent 12 }}",
            wantPattern: "toYaml",
            wantPath:    "env",
        },
        {
            name:        "nested toYaml",
            content:     "{{- toYaml .Values.app.primary.env | nindent 12 }}",
            wantPattern: "toYaml",
            wantPath:    "app.primary.env",
        },
        {
            name:        "toYaml dot inside with",
            content:     "{{- toYaml . | nindent 12 }}",
            withContext: ".Values.volumes",
            wantPattern: "toYaml_dot",
            wantPath:    "volumes",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            usages := analyzeDirectiveContent(tt.content, tt.withContext)
            if len(usages) == 0 {
                t.Fatal("expected at least one usage")
            }

            if usages[0].Pattern != tt.wantPattern {
                t.Errorf("pattern: expected %q, got %q", tt.wantPattern, usages[0].Pattern)
            }
            if usages[0].ValuesPath != tt.wantPath {
                t.Errorf("path: expected %q, got %q", tt.wantPath, usages[0].ValuesPath)
            }
        })
    }
}
```

### Phase 3: Integration Tests

#### 3.1 CLI Integration Tests (`cmd/main_test.go`)

```go
package main

import (
    "bytes"
    "flag"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

var updateGolden = flag.Bool("update-golden", false, "update golden test files")

func TestDetectCommand_Basic(t *testing.T) {
    setupTestEnv(t)

    chartDir := "testdata/charts/basic"

    // Reset global state
    globalCRDRegistry = NewCRDRegistry()

    // Run detection
    result, err := detectConversionCandidatesFull(chartDir)
    if err != nil {
        t.Fatalf("detect failed: %v", err)
    }

    // Verify expected candidates
    expectedPaths := map[string]string{
        "env":          "name",
        "volumes":      "name",
        "volumeMounts": "mountPath",
    }

    for _, c := range result.Candidates {
        expectedKey, ok := expectedPaths[c.SectionName]
        if !ok {
            continue // Extra candidates are OK
        }
        if c.MergeKey != expectedKey {
            t.Errorf("%s: expected key %q, got %q", c.SectionName, expectedKey, c.MergeKey)
        }
        delete(expectedPaths, c.SectionName)
    }

    if len(expectedPaths) > 0 {
        t.Errorf("missing expected candidates: %v", expectedPaths)
    }
}

func TestConvertCommand_DryRun(t *testing.T) {
    setupTestEnv(t)

    // Copy chart to temp directory
    chartDir := copyChart(t, "testdata/charts/basic")

    // Read original files
    origValues, _ := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
    origTemplate, _ := os.ReadFile(filepath.Join(chartDir, "templates", "deployment.yaml"))

    // Run convert with dry-run
    // TODO: Call internal function with dryRun=true

    // Verify files unchanged
    newValues, _ := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
    newTemplate, _ := os.ReadFile(filepath.Join(chartDir, "templates", "deployment.yaml"))

    if !bytes.Equal(origValues, newValues) {
        t.Error("values.yaml was modified in dry-run mode")
    }
    if !bytes.Equal(origTemplate, newTemplate) {
        t.Error("template was modified in dry-run mode")
    }
}

func TestConvertCommand_ActualConversion(t *testing.T) {
    setupTestEnv(t)

    chartDir := copyChart(t, "testdata/charts/basic")

    // Run actual conversion
    globalCRDRegistry = NewCRDRegistry()
    candidates, _ := detectConversionCandidates(chartDir)

    // Convert values.yaml
    valuesPath := filepath.Join(chartDir, "values.yaml")
    // TODO: Call internal conversion function

    // Verify values.yaml converted
    newValues, _ := os.ReadFile(valuesPath)

    // Check that arrays became maps
    if strings.Contains(string(newValues), "env:\n  - name:") {
        t.Error("env should be converted from array to map")
    }

    // Verify helper template created
    helperPath := filepath.Join(chartDir, "templates", "_listmap.tpl")
    if _, err := os.Stat(helperPath); os.IsNotExist(err) {
        t.Error("_listmap.tpl helper should be created")
    }

    // Verify backup created
    backupPath := valuesPath + ".bak"
    if _, err := os.Stat(backupPath); os.IsNotExist(err) {
        t.Error("backup file should be created")
    }
}

func TestLoadCRDCommand_Directory(t *testing.T) {
    configDir := setupTestEnv(t)
    crdsDir := filepath.Join(configDir, "crds")

    // Create temp CRD directory
    srcDir := t.TempDir()
    crdContent := `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: tests.example.com
spec:
  group: example.com
  names:
    kind: Test
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
`
    os.WriteFile(filepath.Join(srcDir, "crd-test.yaml"), []byte(crdContent), 0644)

    // Run load-crd
    err := loadAndStoreCRDsFromDirectory(srcDir, crdsDir)
    if err != nil {
        t.Fatalf("load-crd failed: %v", err)
    }

    // Verify CRD copied to config directory
    destFile := filepath.Join(crdsDir, "crd-test.yaml")
    if _, err := os.Stat(destFile); os.IsNotExist(err) {
        t.Error("CRD should be copied to config directory")
    }
}

func TestConvertIdempotent(t *testing.T) {
    setupTestEnv(t)

    chartDir := copyChart(t, "testdata/charts/basic")

    // Run convert twice
    // First conversion
    // TODO: Call internal conversion function

    // Get state after first conversion
    valuesAfterFirst, _ := os.ReadFile(filepath.Join(chartDir, "values.yaml"))

    // Second conversion
    // TODO: Call internal conversion function again

    // Get state after second conversion
    valuesAfterSecond, _ := os.ReadFile(filepath.Join(chartDir, "values.yaml"))

    // Should be identical
    if !bytes.Equal(valuesAfterFirst, valuesAfterSecond) {
        t.Error("convert should be idempotent")
    }
}
```

### Phase 4: Golden File Tests

```go
func TestDetectGolden(t *testing.T) {
    tests := []struct {
        name       string
        chartDir   string
        crdFiles   []string
        goldenFile string
    }{
        {"basic", "testdata/charts/basic", nil, "detect/basic.txt"},
        {"nested-values", "testdata/charts/nested-values", nil, "detect/nested-values.txt"},
        {"crd-based", "testdata/charts/crd-based", []string{"testdata/crds/prometheus-crd.yaml"}, "detect/crd-based.txt"},
        {"with-partials", "testdata/charts/with-partials", nil, "detect/with-partials.txt"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            setupTestEnv(t)
            globalCRDRegistry = NewCRDRegistry()

            // Load CRDs if specified
            for _, crdFile := range tt.crdFiles {
                if err := globalCRDRegistry.LoadFromFile(crdFile); err != nil {
                    t.Fatalf("failed to load CRD: %v", err)
                }
            }

            // Run detection and capture output
            var buf bytes.Buffer
            // TODO: Capture output to buf

            // Compare to golden file
            expected := goldenFile(t, tt.goldenFile, buf.Bytes(), *updateGolden)
            if !bytes.Equal(expected, buf.Bytes()) {
                t.Errorf("output mismatch:\n%s", buf.String())
            }
        })
    }
}
```

### Phase 5: Test Fixtures

#### Basic Chart (`cmd/testdata/charts/basic/`)

**Chart.yaml:**

```yaml
apiVersion: v2
name: basic
version: 0.1.0
```

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
  selector:
    matchLabels:
      app: { { .Release.Name } }
  template:
    metadata:
      labels:
        app: { { .Release.Name } }
    spec:
      containers:
        - name: app
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          env: { { - toYaml .Values.env | nindent 12 } }
          volumeMounts: { { - toYaml .Values.volumeMounts | nindent 12 } }
      volumes: { { - toYaml .Values.volumes | nindent 8 } }
```

#### Edge Cases Chart (`cmd/testdata/charts/edge-cases/`)

Test empty arrays, conditional patterns, etc.

**values.yaml:**

```yaml
# Empty arrays should still be converted
emptyVolumes: []

# Nested empty
nested:
  containers: []

# Already map-like (should be skipped)
mapStyle:
  myVolume:
    configMap:
      name: my-config
```

**templates/deployment.yaml:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test
spec:
  template:
    spec:
      # Pattern 4: conditional with indent
      {{- if .Values.emptyVolumes }}
        volumes:
      {{ toYaml .Values.emptyVolumes | indent 8 }}
      {{- end }}
```

### Phase 6: Makefile Integration

Add to `Makefile`:

```makefile
.PHONY: test test-short test-coverage test-update-golden

test:
	go test -v ./cmd/...

test-short:
	go test -v -short ./cmd/...

test-coverage:
	go test -v -coverprofile=coverage.out ./cmd/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-update-golden:
	go test -v ./cmd/... -update-golden
```

## Implementation Checklist

### Phase 1: Infrastructure ✅ COMPLETE

- [x] Create `cmd/testdata/` directory structure
- [x] Create `cmd/testutil_test.go` with helpers
- [x] Create basic chart fixture
- [x] Verify test infrastructure works with a simple test

### Phase 2: Unit Tests ✅ COMPLETE

- [x] `cmd/glob_test.go` - Glob pattern matching tests
- [x] `cmd/transform_test.go` - Array transformation tests
- [x] `cmd/template_test.go` - Template rewriting tests
- [x] `cmd/crd_test.go` - CRD registry, loading, parsing, embedded K8s type detection
- [x] `cmd/error_test.go` - Error handling and edge case tests
- [ ] `cmd/analyzer_test.go` - K8s type introspection tests (OPTIONAL - see code examples below)
- [ ] `cmd/parser_test.go` - Template parsing tests (OPTIONAL - see code examples below)

### Phase 3: Integration Tests ✅ COMPLETE

- [x] `cmd/integration_test.go` - Chart loading, detection tests
- [x] `cmd/cli_test.go` - CLI command tests (detect, convert, --recursive, help)
- [x] Add chart fixtures (basic, nested-values, edge-cases, all-patterns, umbrella)

### Phase 4: Golden Files & Polish ✅ COMPLETE

- [x] Set up golden file infrastructure (`cmd/golden_test.go`)
- [x] Create golden files for detect output (basic, nested-values, all-patterns)
- [x] Add Makefile targets (test, test-cover, test-short)
- [ ] Document test running in README (OPTIONAL)

### Remaining Work

**Future (when features implemented):**

1. `--include-charts-dir` tests - Embedded subchart detection
2. `--expand-remote` tests - Tarball expansion
3. Multiple values files (`-f`/`--values`) tests - See below

**Optional enhancements:** 3. `cmd/analyzer_test.go` - K8s type introspection (code examples in Phase 2.1 below) 4. `cmd/parser_test.go` - Template parsing (code examples in Phase 2.3 below)

## Notes

- All tests use `t.Parallel()` where safe
- Use `t.TempDir()` for automatic cleanup
- Use `t.Setenv()` for environment isolation
- Tests requiring network are skipped with `-short`
- Golden file updates require explicit `-update-golden` flag
- The global `globalCRDRegistry` must be reset between tests

## Important Test Cases to Add

### Array Indentation Tests

**CRITICAL**: The plugin must correctly handle YAML arrays where items start at the same indentation as the parent key. This is valid YAML:

```yaml
volumes:
  - name: data
    emptyDir: {}
```

This is equivalent to:

```yaml
volumes:
  - name: data
    emptyDir: {}
```

After conversion, both forms MUST produce map entries indented properly under the parent key:

```yaml
volumes:
  data:
    emptyDir: {}
```

The `transformSingleItemWithIndent` function uses the parent key's column position (`edit.KeyColumn`) to calculate correct indentation, not the array item's indentation. Tests should verify:

1. Arrays with items at same indent as parent key (minio-style)
2. Arrays with items indented under parent key (standard style)
3. Deeply nested arrays with various indentation styles
4. Mixed indentation within the same values.yaml file

### Multiple Values Files Tests

**Feature:** Support for `-f`/`--values` flags to process additional values files beyond the chart's default `values.yaml`.

**Test fixtures needed:**

```
cmd/testdata/charts/multi-values/
├── Chart.yaml
├── values.yaml              # Base values with arrays
├── values-override.yaml     # Override file with arrays
├── values-dev.yaml          # Environment-specific overrides
├── values-prod.yaml         # Different env overrides
└── templates/
    └── deployment.yaml
```

**Test cases:**

1. **Basic override file detection**
   - Detect arrays in base `values.yaml` AND additional `-f` files
   - Report which file each candidate comes from

2. **Override file conversion**
   - Convert arrays in override files to match base chart's new map format
   - Verify backup files created for each modified values file

3. **Multiple override files**
   - Process files in order: `values.yaml`, then `-f` files left-to-right
   - Handle same path in multiple files (last wins semantics)

4. **Path resolution**
   - Relative paths from CWD
   - Relative paths from chart directory
   - Absolute paths
   - Non-existent file error handling

5. **Edge cases**
   - Override file has array not in base chart (standalone detection)
   - Override file has map where base has array (conflict warning)
   - Empty override file
   - Override file with only scalar overrides (no arrays)

6. **Dry-run mode**
   - Verify no override files modified in dry-run
   - Report what would be changed in each file

**Example test values files:**

`values.yaml`:

```yaml
env:
  - name: BASE_VAR
    value: base
volumes:
  - name: config
    configMap:
      name: base-config
```

`values-override.yaml`:

```yaml
env:
  - name: OVERRIDE_VAR
    value: override
  - name: BASE_VAR
    value: overridden
# volumes not specified - inherits from base
```

`values-dev.yaml`:

```yaml
env:
  - name: DEBUG
    value: "true"
extraVolumes: # New array not in base
  - name: dev-data
    emptyDir: {}
```
