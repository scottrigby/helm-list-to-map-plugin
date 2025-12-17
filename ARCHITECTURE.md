# Architecture

This document explains the design decisions behind the list-to-map Helm plugin.

For the problem statement, solution overview, installation, and usage, see [README.md](README.md).

## Overview

The plugin uses Kubernetes API introspection and CRD schema parsing to automatically detect convertible fields:

1. **Parses templates** to find `apiVersion` and `kind` of K8s resources being constructed
2. **Maps to K8s Go types** (e.g., `apps/v1` + `Deployment` → `appsv1.Deployment`) or looks up loaded CRD schemas
3. **Queries merge keys** using the K8s [`strategicpatch` API](#the-patchmergekey-discovery), or parses [`x-kubernetes-list-map-keys`](#crd-schema-parsing) from CRD OpenAPI schemas
4. **Converts values and templates** using a [single generic helper](#single-generic-helper)

## Why K8s API Introspection?

The plugin must determine which arrays are convertible and what field serves as the unique key. Two approaches exist:

1. **Hardcoded rules**: Maintain a list of known K8s fields and their unique keys
2. **API introspection**: Query K8s Go types at runtime to discover this information

We chose introspection because:

- **Completeness**: Covers all K8s types automatically, not just a curated subset
- **Correctness**: Uses the same source of truth that Kubernetes uses
- **Maintainability**: No manual updates needed when K8s APIs change

## The patchMergeKey Discovery

Kubernetes uses Strategic Merge Patch for applying changes to resources. The `k8s.io/apimachinery/pkg/util/strategicpatch` package provides APIs to query merge key information programmatically:

```go
// Get merge key for a slice field using strategicpatch API
patchMeta, _ := strategicpatch.NewPatchMetaFromStruct(corev1.PodSpec{})
_, pm, _ := patchMeta.LookupPatchMetadataForSlice("volumes")
mergeKey := pm.GetPatchMergeKey() // Returns "name"
```

This is the definitive source for unique keys. Examples:

- `volumes` → keyed by `name`
- `containers` → keyed by `name`
- `volumeMounts` → keyed by `mountPath` (not `name`!)
- `ports` → keyed by `containerPort` (not `name`!)

Using the strategicpatch API ensures the plugin uses the same uniqueness semantics as Kubernetes itself.

### Atomic Lists (No Merge Key)

Some K8s list fields intentionally have no merge key - they use "atomic" replacement strategy. This means K8s replaces the entire list rather than merging individual items. Examples:

- `tolerations` - Can have same `key` with different `effect` values
- `sysctls` - Treated as atomic by K8s
- `readinessGates` - Treated as atomic by K8s

The plugin respects this design: fields without merge keys are reported as "arrays without auto-detected unique keys" and require explicit user rules if conversion is desired. This prevents incorrect assumptions about uniqueness semantics.

## Template-First Detection

The plugin analyzes Helm templates to find conversion candidates rather than scanning values.yaml content. This is necessary because:

1. **Empty arrays are valid**: Charts often default to `volumes: []` expecting users to populate them
2. **Template context matters**: The same values path might be used differently in different templates
3. **K8s type resolution**: We need to know what K8s resource type is being constructed to look up the correct field schema

The detection flow:

1. Parse template to extract `apiVersion` and `kind`
2. Map to K8s Go type (e.g., `apps/v1` + `Deployment` → `appsv1.Deployment`)
3. Find template directives (`{{...}}`) and their YAML path context
4. Navigate the K8s type hierarchy to the field at that path
5. Check if field is a slice with a `patchMergeKey` tag
6. If yes, the field is convertible using that key

### Template-Only Candidates

Detection may find template patterns that reference arrays not defined in `values.yaml`. For example, a template may have:

```yaml
{{- with .Values.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
```

Even if the referenced field (e.g., `imagePullSecrets`) is commented out or missing from `values.yaml`, this is still a valid conversion candidate. The plugin:

1. **Detect**: Reports these separately as "Template patterns without values.yaml entries"
2. **Convert**: Still updates the template to use map-style syntax, making it "map-ready" for when users override this field
3. **User guidance**: Reminds users to update any comments or documentation describing the field format

This behavior ensures charts are fully prepared for map-based overrides even for optional fields.

## Single Generic Helper

All converted fields use the same helper template regardless of K8s type:

```yaml
{
  {
    - include "chart.listmap.render" (dict "items" .Values.volumes "key" "name" "section" "volumes"),
  },
}
```

The helper accepts:

- `items`: The map from values.yaml
- `key`: The patchMergeKey field name to inject
- `section`: The YAML section name to output

This works because the transformation is structurally identical for all list types: iterate map entries, inject the key field, output as YAML list.

## Template Rewriting Patterns

The `convert` command rewrites existing Helm templates to use the new map-based approach. It supports multiple common template patterns found in real-world charts:

### Pattern 1: Direct toYaml with nindent

```yaml
# Before
volumes:
  {{- toYaml .Values.volumes | nindent 2 }}

# After
{{- include "chart.listmap.render" (dict "items" .Values.volumes "key" "name" "section" "volumes") }}
```

### Pattern 2: with block

```yaml
# Before
{{- with .Values.volumes }}
volumes:
  {{- toYaml . | nindent 2 }}
{{- end }}

# After
{{- include "chart.listmap.render" (dict "items" .Values.volumes "key" "name" "section" "volumes") }}
```

### Pattern 3: range loop

```yaml
# Before
volumes:
  {{- range .Values.volumes }}
  - name: {{ .name }}
    # ...
  {{- end }}

# After
{{- include "chart.listmap.render" (dict "items" .Values.volumes "key" "name" "section" "volumes") }}
```

### Pattern 4: Conditional with indent

```yaml
# Before
{{- if .Values.volumes }}
  volumes:
{{ toYaml .Values.volumes | indent 4 }}
{{- end }}

# After
{{- include "chart.listmap.render" (dict "items" .Values.volumes "key" "name" "section" "volumes") }}
```

This pattern is common in charts like kube-prometheus-stack.

### Pattern 5: Conditional with nindent

```yaml
# Before
{{- if .Values.volumes }}
  volumes:
  {{- toYaml .Values.volumes | nindent 2 }}
{{- end }}

# After
{{- include "chart.listmap.render" (dict "items" .Values.volumes "key" "name" "section" "volumes") }}
```

### Pattern 6: Existing specialized helpers

```yaml
# Before (from previous plugin versions)
{{- include "chart.volumes.render" (dict "volumes" (index .Values "volumes")) }}

# After
{{- include "chart.listmap.render" (dict "items" .Values.volumes "key" "name" "section" "volumes") }}
```

All patterns are matched using regex with multiline mode, handling variations in whitespace and formatting.

## Umbrella Chart Support

The plugin supports umbrella charts (charts that aggregate multiple subcharts via dependencies) with the `--recursive` flag.

### How It Works

```bash
helm list-to-map convert --chart ./umbrella-chart --recursive
```

1. **Parse Chart.yaml**: Extract dependencies with `file://` repository references
2. **Convert Subcharts**: For each `file://` dependency, navigate to the source location and run conversion
3. **Track Changes**: Record which paths were converted in each subchart
4. **Update Umbrella Values**: Convert matching arrays in umbrella's `values.yaml`

### Why file:// Only?

The `--recursive` flag only processes `file://` dependencies because:

- **Source Control**: `file://` paths point to source files you can modify
- **Persistence**: Changes survive `helm dependency update`
- **Workflow Fit**: Umbrella charts with local subcharts are a common monorepo pattern

Remote dependencies (Helm repos, OCI registries) are not converted because:

- **No Source Access**: You can't modify files in a remote repository
- **Ephemeral Changes**: `helm dependency update` overwrites local modifications
- **Ownership**: Community charts shouldn't be locally modified

### Umbrella Values Update

When a subchart path is converted (e.g., `deployment.env` in `judge-api`), the umbrella's `values.yaml` must also be updated:

```yaml
# Before (umbrella values.yaml)
judge-api:
  deployment:
    env:
      - name: MY_VAR
        value: "foo"

# After
judge-api:
  deployment:
    env:
      MY_VAR:
        value: "foo"
```

The plugin tracks converted paths per subchart and automatically applies matching updates to the umbrella.

### Subchart Processing Flags

The plugin provides three flags for processing different types of subcharts:

| Flag                   | Processes                            | Use Case                             |
| ---------------------- | ------------------------------------ | ------------------------------------ |
| `--recursive`          | file:// dependencies from Chart.yaml | Umbrella charts with local subcharts |
| `--include-charts-dir` | Directories in charts/               | Vendored/embedded subcharts          |
| `--expand-remote`      | .tgz files in charts/                | Remote dependencies (with warnings)  |

**Decision Matrix:**

| Dependency Type       | Default | --recursive | --include-charts-dir | --expand-remote     |
| --------------------- | ------- | ----------- | -------------------- | ------------------- |
| file:// in Chart.yaml | Skip    | ✓ Convert   | Skip                 | Skip                |
| Directory in charts/  | Skip    | Skip        | ✓ Convert            | Skip                |
| .tgz in charts/       | Skip    | Skip        | Skip                 | ✓ Extract & convert |

Flags can be combined. Deduplication handles overlaps (e.g., file:// pointing to charts/ directory).

**Important:** Changes to --expand-remote dependencies are lost on `helm dependency update`. See [ROADMAP.md](ROADMAP.md#future-enhancements-dependency-handling) for future improvements.

## CRD Schema Parsing

Custom Resource Definitions (CRDs) are not part of the K8s API packages, but modern CRDs include OpenAPI v3 schemas with list-type annotations. The plugin extracts these to enable automatic detection for Custom Resources.

### Explicit List Annotations

CRD YAML files contain OpenAPI schemas with Kubernetes-specific extensions:

```yaml
# From a CRD's spec.versions[].schema.openAPIV3Schema
spec:
  properties:
    hostAliases:
      type: array
      items:
        properties:
          ip: ...
          hostnames: ...
      x-kubernetes-list-type: map
      x-kubernetes-list-map-keys:
        - ip
```

The plugin parses these annotations to determine:

- **`x-kubernetes-list-type: map`**: Indicates the list has unique keys
- **`x-kubernetes-list-map-keys`**: Specifies which field(s) form the unique key

### Embedded K8s Type Detection

Many CRDs embed standard Kubernetes types (like `Container`, `Volume`, `VolumeMount`) without explicitly defining `x-kubernetes-list-map-keys`. For example, the Prometheus Operator's `Alertmanager` CRD has a `spec.containers` field that embeds `corev1.Container`, but the CRD schema doesn't include the list annotation.

The plugin detects these embedded types by comparing CRD schema field signatures against actual K8s API types:

```go
// At init time, build signatures from real K8s types via reflection
k8sTypeRegistry = []k8sTypeSignature{
    {TypeName: "Container", MergeKey: "name", FieldNames: extractFieldNames(corev1.Container{})},
    {TypeName: "Volume", MergeKey: "name", FieldNames: extractFieldNames(corev1.Volume{})},
    {TypeName: "VolumeMount", MergeKey: "mountPath", FieldNames: extractFieldNames(corev1.VolumeMount{})},
    // ...
}
```

When parsing a CRD array schema, the plugin:

1. Extracts field names from the CRD's `items.properties`
2. Compares against each K8s type signature
3. If ≥50% of CRD fields match a K8s type AND the merge key field exists, it's a match
4. Uses the matched type's merge key for conversion

This approach:

- **Uses real K8s types**: Field names come from `k8s.io/api` via reflection, not hardcoded lists
- **Handles K8s updates**: When you update the K8s API dependency, field signatures update automatically
- **Requires merge key mapping**: The merge key for each type must still be specified, as it's defined on the parent struct field (e.g., `PodSpec.Containers`), not on the `Container` type itself

### Why Not Use strategicpatch for Embedded K8s Types in CRDs?

For direct K8s resources (Deployment, Pod, etc.), we use `strategicpatch.LookupPatchMetadataForSlice()` to get merge keys programmatically. This works because we have Go types to query.

For CRDs with **embedded K8s core types** (like `spec.containers` containing `corev1.Container`), we only have YAML schemas - no Go types exist for the CRD itself. The `strategicpatch` API requires a Go struct to query:

```go
// This works for K8s types (we have Go types)
patchMeta, _ := strategicpatch.NewPatchMetaFromStruct(corev1.PodSpec{})

// This doesn't work for CRDs (no Go type exists)
// CRDs only have YAML schemas, not Go structs
```

The `k8sTypeRegistry` bridges this gap specifically for **detecting embedded K8s core types within CRD schemas**. It matches CRD schema field names against known K8s type signatures. When a CRD's `spec.containers` field has properties matching `corev1.Container` (name, image, env, volumeMounts, etc.), we can infer the merge key should be `"name"`.

This only applies to embedded core types. CRD-specific fields (not based on K8s types) must either:

- Have explicit `x-kubernetes-list-map-keys` in the CRD schema, or
- Be manually configured via `add-rule`

| API               | Works On     | Used For                        |
| ----------------- | ------------ | ------------------------------- |
| `strategicpatch`  | Go types     | Direct K8s resources            |
| `k8sTypeRegistry` | YAML schemas | Embedded K8s core types in CRDs |

Currently detected embedded types:

| Type                     | Merge Key       | Common Field Names         |
| ------------------------ | --------------- | -------------------------- |
| Container                | `name`          | containers, initContainers |
| Volume                   | `name`          | volumes                    |
| VolumeMount              | `mountPath`     | volumeMounts               |
| EnvVar                   | `name`          | env                        |
| ContainerPort            | `containerPort` | ports                      |
| TopologySpreadConstraint | `topologyKey`   | topologySpreadConstraints  |
| HostAlias                | `ip`            | hostAliases                |
| VolumeDevice             | `devicePath`    | volumeDevices              |
| ResourceClaim            | `name`          | claims                     |
| LocalObjectReference     | `name`          | imagePullSecrets           |

Note: Types like `Toleration` and `Sysctl` are intentionally excluded. Kubernetes uses atomic replacement for these fields (no merge key), so they require explicit user rules if conversion is desired.

### Loading CRDs

CRDs are loaded using the `load-crd` command and stored in the plugin's config directory:

```bash
# Load from local file
helm list-to-map load-crd ./monitoring-crds.yaml

# Load from URL
helm list-to-map load-crd https://raw.githubusercontent.com/.../crd.yaml

# View loaded CRDs
helm list-to-map list-crds -v
```

CRDs are stored in `$HELM_CONFIG_HOME/list-to-map/crds/` and automatically loaded when running `detect` or `convert`.

## Manual Rules

For cases where automatic detection doesn't work, users can define rules manually:

- **CRDs without schema annotations**: Many CRDs don't define `x-kubernetes-list-map-keys` in their OpenAPI schema
- **Unavailable CRD definitions**: When CRD YAML files aren't accessible
- **Custom conversion needs**: When you want to convert a field that isn't auto-detected

```bash
helm list-to-map add-rule --path='istio.virtualService.http[]' --uniqueKey=name
```

User rules are stored in `$HELM_CONFIG_HOME/list-to-map/config.yaml` and supplement the automatic detection.

## Testability & Interfaces

The codebase uses interfaces to abstract external dependencies (filesystem, CRD registry) to enable unit testing with mocks instead of touching real I/O.

### FileSystem Interface

Package functions that read or write files accept a `FileSystem` interface:

```go
// pkg/fs/fs.go
type FileSystem interface {
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, data []byte, perm os.FileMode) error
    Stat(path string) (os.FileInfo, error)
    WalkDir(root string, fn fs.WalkDirFunc) error
}
```

**Production use** (real filesystem):

```go
template.RewriteTemplatesWithBackups(fs.OSFileSystem{}, chartPath, paths, backupExt, backups)
```

**Test use** (mock filesystem):

```go
mockFS := NewMockFileSystem()
mockFS.files["/test/values.yaml"] = []byte("volumes: []")
template.RewriteTemplatesWithBackups(mockFS, "/test", paths, ".bak", nil)
// No real files created - fully isolated test
```

This enables testing file operations without:

- Creating temporary directories
- Cleaning up test artifacts
- Potential race conditions from parallel tests
- Dependency on filesystem state

### Registry Interface

The CRD registry implements a `Registry` interface to allow mocking CRD lookups:

```go
// pkg/crd/interface.go
type Registry interface {
    LoadFromFile(path string) error
    LoadFromURL(url string) error
    HasType(apiVersion, kind string) bool
    GetFieldInfo(apiVersion, kind, yamlPath string) *CRDFieldInfo
    // ...
}
```

This allows tests to inject pre-populated CRD metadata without loading real CRD files or making HTTP requests.

### Design Rationale

**Why interfaces instead of direct calls?**

The plugin previously called `os.ReadFile`, `os.WriteFile`, etc. directly throughout the codebase. This made unit tests difficult because:

1. **Filesystem state**: Tests had to create real files, manage cleanup, and deal with potential conflicts
2. **Slow tests**: Real I/O is orders of magnitude slower than in-memory operations
3. **Hard to test error paths**: Difficult to simulate "file not found" or "permission denied" scenarios

With interfaces:

1. **Fast**: Tests run entirely in-memory
2. **Isolated**: No shared state between tests
3. **Comprehensive**: Easy to test error conditions by returning errors from mocks
4. **Deterministic**: No flakiness from filesystem timing or permissions

**Implementation strategy:**

- `pkg/` functions accept interfaces (testable, reusable)
- `cmd/` code passes `OSFileSystem{}` to `pkg/` (production use)
- `pkg/` unit tests use mock implementations (fast, isolated)
- `cmd/` integration tests use real filesystem (test actual behavior)

This follows dependency injection principles while keeping the global registry available in `cmd/` for backward compatibility. See [Testing Strategy](#testing-strategy) for details on when to use mocks vs real dependencies.

## Testing Strategy

The codebase uses different testing approaches depending on what's being tested. Understanding when to use mocks vs real dependencies is crucial for maintainability.

### Test Types

**Unit Tests** (`pkg/*_test.go`)

- Test individual functions/packages in isolation
- Use mock implementations (MockFileSystem, MockRegistry)
- Fast execution (in-memory, no I/O)
- Focus on logic correctness

Example:

```go
// pkg/template/rewrite_test.go
func TestRewriteTemplates(t *testing.T) {
    mockFS := fs.NewMockFileSystem()
    mockFS.files["/chart/templates/deployment.yaml"] = []byte("env:\n{{- toYaml .Values.env }}")

    err := template.RewriteTemplates(mockFS, "/chart", paths, ".bak")
    // Verify in-memory state changed, no disk I/O
}
```

**Integration Tests** (`cmd/*_test.go` - calling internal functions)

- Test multiple components working together
- Use real filesystem via standard `os.*` calls
- Use `t.TempDir()` for isolation
- Call internal functions directly (e.g., `detectConversionCandidatesFull`)

Example:

```go
// cmd/integration_test.go, cmd/dependency_test.go
func TestDetectCommand(t *testing.T) {
    setupTestEnv(t)  // Sets HELM_CONFIG_HOME
    chartPath := copyChart(t, "testdata/charts/basic")  // Real files

    result, err := detectConversionCandidatesFull(chartPath)  // Internal function

    // Verify detection logic with real chart files
}
```

**End-to-End Tests** (`cmd/*_test.go` - executing binary)

- Test complete user workflows via CLI
- Build binary with `go build`, execute with `exec.Command`
- Use real filesystem, real Helm charts
- Verify both command output and file modifications

Example:

```go
// cmd/cli_test.go, cmd/subchart_integration_test.go
func TestCLIConvertActual(t *testing.T) {
    binPath := buildTestBinary(t)  // Builds actual binary
    chartPath := copyChart(t, "testdata/charts/basic")

    cmd := exec.Command(binPath, "convert", "--chart", chartPath)  // Exec binary
    output, err := cmd.CombinedOutput()

    // Verify both CLI output and file changes
    convertedValues, _ := os.ReadFile(filepath.Join(chartPath, "values.yaml"))
    // Check values.yaml was actually converted
}
```

**Note:** Both integration and E2E tests live in `cmd/*_test.go` files. The distinction is:

- **Integration tests** call internal functions (faster, easier to debug)
- **E2E tests** execute the compiled binary (slower, tests actual user experience)

### When to Use MockFileSystem

**Use mocks in unit tests of `pkg/` functions:**

```go
// Testing pkg/template functions
func TestEnsureHelpers(t *testing.T) {
    mockFS := fs.NewMockFileSystem()

    created := template.EnsureHelpersWithReport(mockFS, "/chart")

    // Fast: no real files created
    // Isolated: parallel tests don't conflict
    // Comprehensive: easy to test error conditions
}
```

**Use real filesystem in `cmd/` integration/e2e tests:**

```go
// Testing CLI behavior
func TestConvertCommand(t *testing.T) {
    chartPath := copyChart(t, "testdata/charts/basic")

    // Use real os.ReadFile, os.WriteFile, etc.
    // Tests actual end-to-end behavior users will experience
}
```

### Production Code Organization

**Production code** = All non-test code shipped to users:

- `cmd/*.go` - CLI layer (commands, entry points, CLI-specific logic)
- `pkg/*/*.go` - Domain logic (reusable library code, algorithms)

Both are production code! The distinction is architectural:

- `pkg/` functions accept `FileSystem` interface (testable with mocks)
- `cmd/` code passes `fs.OSFileSystem{}` to `pkg/` functions (real filesystem)
- `cmd/` integration tests use `os.*` directly (test real behavior)

### Test File Organization

```
cmd/
├── detect.go                  # Production: CLI command
├── detect_test.go             # Integration: tests detect command
├── cli_test.go                # E2E: tests CLI via exec.Command
├── subchart_integration_test.go  # E2E: tests subchart workflows
└── testdata/                  # Fixtures for integration tests
    └── charts/

pkg/template/
├── rewrite.go                 # Production: template rewriting logic
├── rewrite_test.go            # Unit: tests with MockFileSystem
└── helper.go                  # Production: helper generation
```

### Design Rationale

**Why different approaches?**

Unit tests with mocks:

- ✅ Fast (in-memory)
- ✅ Isolated (no shared state)
- ✅ Easy to test error paths
- ✅ Parallel execution safe
- ❌ Don't test real filesystem behavior

Integration/E2E tests with real filesystem:

- ✅ Test actual user experience
- ✅ Catch real-world issues
- ✅ Verify CLI output and file changes
- ❌ Slower
- ❌ Require cleanup (t.TempDir() handles this)

Both are necessary:

- Unit tests ensure individual functions work correctly
- Integration tests ensure components work together
- E2E tests ensure the tool works as users expect

### Running Tests

```bash
# All tests (explicit paths to avoid nested projects)
go test -v ./cmd/... ./pkg/...

# Only unit tests (fast, for development)
go test -v ./pkg/...

# Only integration/E2E tests
go test -v ./cmd/...

# Only E2E tests (binary execution)
go test -v ./cmd/... -run 'TestCLI|TestSubchart'

# Skip slow tests during development
go test -v -short ./cmd/... ./pkg/...

# Using Makefile targets (see Makefile for available targets)
make test              # All tests
make test-unit         # Unit tests only
make test-integration  # Integration/E2E tests only
```

## File Overview

**cmd/** - CLI layer:
| File | Purpose |
| --- | --- |
| `root.go` | main(), usage(), command routing, types |
| `detect.go` | detect + recursive-detect commands |
| `convert.go` | convert + recursive-convert commands |
| `load_crd.go` | load-crd command |
| `list_crds.go` | list-crds command |
| `add_rule.go` | add-rule command |
| `list_rules.go` | rules command |
| `helpers.go` | findChartRoot, loadValuesNode, matchRule, etc. |
| `options.go` | Options structs for all commands |

**pkg/** - Domain logic:
| Package | Purpose |
| --- | --- |
| `pkg/k8s/` | K8s type introspection, field schema navigation, merge key detection |
| `pkg/crd/` | CRD registry, loading, metadata extraction, embedded type detection |
| `pkg/parser/` | Template parsing, directive extraction |
| `pkg/transform/` | Array-to-map transformation |
| `pkg/template/` | Template rewriting, helper generation |
| `pkg/detect/` | Shared types (DetectedCandidate) |
| `pkg/fs/` | FileSystem interface for testability |

## Alternatives Considered

### Hardcoded Field Mappings

Early versions maintained lists of known fields:

```go
var knownFields = map[string]string{
    "env": "name",
    "volumes": "name",
    "volumeMounts": "name",
    "ports": "name",
}
```

**Flaw**: Incomplete (misses many K8s fields), incorrect (volumeMounts uses `mountPath`, ports uses `containerPort`), and requires manual maintenance.

### Heuristic Key Detection

Attempted to find unique keys by scanning array items for fields that:

- Are present in all items
- Have unique values across items
- Match common patterns like "name", "id", "key"

**Flaw**: Unreliable. Many fields have `name` but it's not the unique key. Cannot detect anything for empty arrays. The heuristics were complex and still wrong for cases like `volumeMounts`.

### Specialized Helper Templates

Created separate helpers for each field type:

```
_env.tpl
_volumes.tpl
_volumeMounts.tpl
_ports.tpl
```

**Flaw**: Unnecessary complexity. All helpers did the same thing (iterate map, inject key field). The only difference was the field name, which can be parameterized.

### Renderer Type Inference

Tried to infer "renderer type" from YAML path suffixes:

```go
if strings.HasSuffix(path, ".env") {
    return "env"
}
if strings.HasSuffix(path, ".volumeMounts") {
    return "volumeMounts"
}
```

**Flaw**: Coupled template generation to field names rather than structure. Required maintaining a finite list of known types. The renderer concept was unnecessary once we had patchMergeKey.

### Content-Based Detection Only

Original approach scanned values.yaml for arrays with items containing unique-looking fields.

**Flaw**: Cannot detect empty arrays (`volumes: []`), which are common chart defaults. Also couldn't determine K8s context to validate field schema.
