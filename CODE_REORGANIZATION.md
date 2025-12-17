# Code Reorganization Plan

This document analyzes the current codebase structure and proposes improvements for readability, maintainability, and idiomatic Go patterns.

## Current State Analysis

### File Structure Overview

| File              | Lines | Purpose                                                                     |
| ----------------- | ----- | --------------------------------------------------------------------------- |
| `cmd/main.go`     | 2693  | Monolithic: CLI, commands, YAML transformation, template rewriting, helpers |
| `cmd/analyzer.go` | 827   | K8s type introspection, conversion candidate detection                      |
| `cmd/crd.go`      | 724   | CRD registry, loading, metadata extraction, embedded type detection         |
| `cmd/parser.go`   | 435   | Template parsing, directive extraction, values usage analysis               |

### Key Issues Identified

#### 1. Monolithic main.go (Critical)

`main.go` at 2693 lines contains 6+ distinct concerns:

1. **CLI Framework** (lines 68-107, 109-144): `main()`, `usage()`, command routing
2. **Command Implementations** (lines 146-652, 654-1000+): `runDetect`, `runConvert`, `runAddRule`, `runListRules`, `runLoadCRD`, `runListCRDs`
3. **Recursive Chart Handling** (lines 1463-1838): `runRecursiveDetect`, `runRecursiveConvert`, `convertSubchartAndTrack`, `updateUmbrellaValues`
4. **YAML Processing** (lines 1840-2063): `loadValuesNode`, `findArrayEdits`, `generateMapReplacement`, `applyLineEdits`
5. **Array Transformation** (lines 2200-2429): `transformArrayToMap`, `transformSingleItem`, `transformSingleItemWithIndent`
6. **Template Rewriting** (lines 2468-2688): `checkTemplatePatterns`, `rewriteTemplatesWithBackups`, `replaceListBlocks`, helper generation

#### 2. Global State Pollution

```go
// main.go - Package-level mutable state
var (
    subcmd           string
    chartDir         string
    dryRun           bool
    backupExt        string
    configPath       string
    recursive        bool
    conf             Config
    transformedPaths []PathInfo
)

// crd.go
var globalCRDRegistry = NewCRDRegistry()

// analyzer.go
var kubeTypeRegistry map[string]reflect.Type

// crd.go
var k8sTypeRegistry []k8sTypeSignature
```

This global state:

- Makes testing difficult (requires reset between tests)
- Creates hidden coupling between functions
- Prevents concurrent execution

#### 3. Code Duplication

- Chart walking logic duplicated in `runRecursiveDetect` and `runRecursiveConvert`
- Candidate filtering repeated in `runDetect`, `runConvert`, and recursive variants
- Template pattern checking duplicated across commands

#### 4. Non-Idiomatic Go Patterns

1. **Manual sorting instead of `sort.Slice`** (main.go:2078-2084):

   ```go
   // Current: Bubble sort
   for i := 0; i < len(sortedEdits)-1; i++ {
       for j := i + 1; j < len(sortedEdits); j++ {
           if sortedEdits[i].KeyLine < sortedEdits[j].KeyLine {
               sortedEdits[i], sortedEdits[j] = sortedEdits[j], sortedEdits[i]
           }
       }
   }

   // Idiomatic:
   sort.Slice(sortedEdits, func(i, j int) bool {
       return sortedEdits[i].KeyLine > sortedEdits[j].KeyLine
   })
   ```

2. **Flag parsing without cobra/kong patterns** - Manual flag.FlagSet per command instead of structured command options

3. **Mixed error handling** - Some functions call `fatal()`, others return errors, making composition difficult

4. **Stringly-typed paths** - YAML paths as strings instead of typed `[]string` or custom type

#### 5. Missing Abstractions

- No interfaces for file I/O (makes testing require real files)
- No interface for CRD registry (hard to mock)
- Regex patterns compiled repeatedly instead of once

---

## Proposed Reorganization

### Phase 1: Extract Core Packages (Sonnet)

**Recommended Model: Sonnet** - Mechanical extraction, clear boundaries

Create focused packages under `pkg/`:

```
pkg/
├── chart/
│   ├── chart.go           # Chart struct, FindRoot(), LoadDependencies()
│   ├── values.go          # LoadValuesNode(), values.yaml operations
│   └── walk.go            # WalkSubcharts(), recursive chart traversal
├── transform/
│   ├── edit.go            # ArrayEdit struct, findArrayEdits()
│   ├── apply.go           # applyLineEdits(), line-based editing
│   ├── item.go            # transformSingleItem(), transformSingleItemWithIndent()
│   └── generate.go        # generateMapReplacement(), generateFieldYAML()
├── template/
│   ├── rewrite.go         # replaceListBlocks(), pattern replacement
│   ├── helper.go          # listMapHelper(), ensureHelpers()
│   └── patterns.go        # Compiled regex patterns (initialize once)
├── detect/
│   ├── detect.go          # detectConversionCandidates(), core detection logic
│   ├── filter.go          # checkCandidatesInValues(), filtering logic
│   └── result.go          # DetectionResult, DetectedCandidate structs
├── config/
│   ├── config.go          # Config, Rule structs
│   ├── load.go            # LoadConfig(), defaultUserConfigPath()
│   └── match.go           # matchGlob(), matchRule()
└── k8s/
    ├── types.go           # Type registry, resolveKubeAPIType()
    ├── fields.go          # navigateFieldSchema(), findFieldByJSONTag()
    └── merge.go           # getMergeKeyFromStrategicPatch()
```

**Example extraction - `pkg/transform/apply.go`:**

```go
package transform

import (
    "sort"
    "strings"
)

// ApplyEdits applies line-based edits to preserve original formatting
func ApplyEdits(original []byte, edits []ArrayEdit) []byte {
    if len(edits) == 0 {
        return original
    }

    lines := strings.Split(string(original), "\n")

    // Sort by line number descending (edit from bottom to top)
    sorted := make([]ArrayEdit, len(edits))
    copy(sorted, edits)
    sort.Slice(sorted, func(i, j int) bool {
        return sorted[i].KeyLine > sorted[j].KeyLine
    })

    for _, edit := range sorted {
        lines = applyEdit(lines, edit)
    }

    return []byte(strings.Join(lines, "\n"))
}
```

### Phase 2: Eliminate Global State (Sonnet)

**Recommended Model: Sonnet** - Structural refactoring with clear patterns

#### 2.1 Command Options Structs

Replace global flags with options structs:

```go
// cmd/options.go
type DetectOptions struct {
    ChartDir   string
    ConfigPath string
    Recursive  bool
    Verbose    bool
}

type ConvertOptions struct {
    ChartDir   string
    ConfigPath string
    DryRun     bool
    BackupExt  string
    Recursive  bool
}
```

#### 2.2 Application Context

Create an App struct that holds dependencies:

```go
// cmd/app.go
type App struct {
    CRDRegistry  *crd.Registry
    Config       *config.Config
    Stdout       io.Writer
    Stderr       io.Writer
}

func NewApp() *App {
    return &App{
        CRDRegistry: crd.NewRegistry(),
        Stdout:      os.Stdout,
        Stderr:      os.Stderr,
    }
}

func (a *App) RunDetect(opts DetectOptions) error {
    // All state flows through opts and a
}
```

#### 2.3 Registry Injection

Pass registries explicitly instead of using globals:

```go
// Before (global)
func detectConversionCandidates(chartRoot string) ([]DetectedCandidate, error) {
    // Uses globalCRDRegistry implicitly
}

// After (injected)
func detectConversionCandidates(chartRoot string, reg *crd.Registry) ([]DetectedCandidate, error) {
    // Uses passed registry
}
```

### Phase 3: Introduce Interfaces (Sonnet)

**Recommended Model: Sonnet** - Interface extraction is mechanical

#### 3.1 File System Interface

```go
// pkg/fs/fs.go
type FileSystem interface {
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, data []byte, perm os.FileMode) error
    Stat(path string) (os.FileInfo, error)
    WalkDir(root string, fn fs.WalkDirFunc) error
}

// Real implementation
type OSFileSystem struct{}

// Test implementation
type MemoryFileSystem struct {
    Files map[string][]byte
}
```

#### 3.2 CRD Registry Interface

```go
// pkg/crd/interface.go
type Registry interface {
    LoadFromFile(path string) error
    LoadFromURL(url string) error
    LoadFromDirectory(dir string) error
    HasType(apiVersion, kind string) bool
    GetFieldInfo(apiVersion, kind, yamlPath string) *FieldInfo
    IsArrayField(apiVersion, kind, yamlPath string) bool
    ListTypes() []string
}
```

### Phase 4: Command Consolidation (Sonnet)

**Recommended Model: Sonnet** - Straightforward refactoring

#### 4.1 Separate Command Files

```
cmd/
├── main.go              # Entry point only (~50 lines)
├── app.go               # App struct, dependency injection
├── options.go           # Command option structs
├── detect.go            # runDetect command
├── convert.go           # runConvert command
├── rules.go             # runAddRule, runListRules commands
├── crd_commands.go      # runLoadCRD, runListCRDs commands
└── recursive.go         # runRecursiveDetect, runRecursiveConvert
```

#### 4.2 Simplified main.go

```go
package main

func main() {
    app := NewApp()
    if err := app.Run(os.Args[1:]); err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }
}
```

### Phase 5: Refactor Specific Code Patterns (Haiku/Sonnet)

#### 5.1 Precompile Regex Patterns (Haiku)

**Recommended Model: Haiku** - Simple, mechanical change

```go
// pkg/template/patterns.go
var (
    // Compile once at package init
    patternToYaml = regexp.MustCompile(`\{\{-?\s*toYaml\s+\.Values\.`)
    patternIndent = regexp.MustCompile(`nindent\s*(\d+)`)
    // etc.
)

// replaceListBlocks uses pre-compiled patterns
func replaceListBlocks(tpl, dotPath, mergeKey string) (string, bool) {
    // Use patternToYaml.ReplaceAllStringFunc(...)
}
```

#### 5.2 Typed YAML Paths (Sonnet)

**Recommended Model: Sonnet** - Requires careful type design

```go
// pkg/yaml/path.go
type YAMLPath []string

func (p YAMLPath) String() string {
    return strings.Join(p, ".")
}

func (p YAMLPath) Append(segment string) YAMLPath {
    return append(p, segment)
}

func ParsePath(s string) YAMLPath {
    return strings.Split(s, ".")
}
```

#### 5.3 Error Handling Consistency (Sonnet)

**Recommended Model: Sonnet** - Pattern application across codebase

```go
// Remove fatal() calls from non-main functions
// Before:
func runDetect() {
    root, err := findChartRoot(chartDir)
    if err != nil {
        fatal(err)  // Exits program
    }
}

// After:
func (a *App) RunDetect(opts DetectOptions) error {
    root, err := chart.FindRoot(opts.ChartDir)
    if err != nil {
        return fmt.Errorf("finding chart root: %w", err)
    }
    // ...
}
```

---

## Refactoring Priorities

### High Priority (Do First)

| Task                                  | Model  | Effort | Impact                              |
| ------------------------------------- | ------ | ------ | ----------------------------------- |
| Extract `pkg/transform/`              | Sonnet | Medium | High - Core functionality isolation |
| Extract `pkg/template/`               | Sonnet | Medium | High - Separates template concerns  |
| Remove global flags → Options structs | Sonnet | Low    | High - Enables testing              |
| Precompile regex patterns             | Haiku  | Low    | Medium - Performance improvement    |

### Medium Priority

| Task                      | Model  | Effort | Impact                            |
| ------------------------- | ------ | ------ | --------------------------------- |
| Create App context struct | Sonnet | Medium | High - Eliminates global state    |
| Extract `pkg/chart/`      | Sonnet | Medium | Medium - Chart handling isolation |
| Split command files       | Sonnet | Low    | Medium - Readability              |
| FileSystem interface      | Sonnet | Medium | High - Testability                |

### Lower Priority

| Task                         | Model  | Effort | Impact               |
| ---------------------------- | ------ | ------ | -------------------- |
| Typed YAML paths             | Sonnet | Medium | Low - Type safety    |
| CRD Registry interface       | Sonnet | Low    | Medium - Testability |
| Move analyzer.go to pkg/k8s/ | Sonnet | Medium | Low - Organization   |
| Move crd.go to pkg/crd/      | Sonnet | Medium | Low - Organization   |

---

## Specific Code Improvements

### 1. Sort Algorithm (main.go:2078-2084)

**Current:** O(n²) bubble sort
**Fix:** Use `sort.Slice` - O(n log n)
**Model:** Haiku
**Lines affected:** 7

### 2. Repeated Regex Compilation (template rewriting)

**Current:** Regex compiled on every function call
**Fix:** Package-level compiled patterns
**Model:** Haiku
**Lines affected:** ~20

### 3. Chart Walking Duplication (recursive.go)

**Current:** Similar logic in `runRecursiveDetect` and `runRecursiveConvert`
**Fix:** Extract `WalkSubcharts(root string, fn func(subchartPath string) error)`
**Model:** Sonnet
**Lines affected:** ~100

### 4. needsQuoting Function (main.go:2025-2037)

**Current:** Manual character checks
**Fix:** Use `strconv.Quote` behavior or yaml.v3 quoting detection
**Model:** Haiku
**Lines affected:** 15

```go
// Current
func needsQuoting(s string) bool {
    for _, c := range s {
        if c == ':' || c == '#' || ... {
            return true
        }
    }
    return false
}

// Better - leverage yaml library
func needsQuoting(s string) bool {
    var buf bytes.Buffer
    enc := yaml.NewEncoder(&buf)
    enc.Encode(s)
    quoted := buf.String()
    return strings.HasPrefix(quoted, `"`) || strings.HasPrefix(quoted, `'`)
}
```

### 5. HTTP Client Configuration (crd.go:265)

**Current:** Uses default `http.Get` (no timeout)
**Fix:** Create configured client with timeout
**Model:** Haiku

```go
// Current
resp, err := http.Get(url)

// Better
var httpClient = &http.Client{Timeout: 30 * time.Second}

func (r *CRDRegistry) LoadFromURL(url string) error {
    resp, err := httpClient.Get(url)
    // ...
}
```

### 6. Helm-Specific Patterns

**Template Helper Generation (main.go:2669-2688)**

The current helper uses `sortAlpha` which is Helm-specific. Consider:

- Documenting this Helm dependency explicitly
- Adding comments about Helm functions used (`keys`, `sortAlpha`, `get`, `quote`)

**Model:** Haiku (documentation), Sonnet (if making configurable)

---

## Implementation Order

### Sprint 1: Foundation (Sonnet)

1. Create `pkg/transform/` package - extract array transformation
2. Create `pkg/template/` package - extract template rewriting
3. Replace global flags with options structs
4. Add `sort.Slice` fix

### Sprint 2: State Management (Sonnet)

1. Create App context struct
2. Inject CRD registry instead of global
3. Split command implementations into separate files
4. Ensure all tests pass with new structure

### Sprint 3: Interfaces & Testing (Sonnet)

1. Create FileSystem interface
2. Create CRD Registry interface
3. Improve test coverage with mocked dependencies
4. Add integration tests for refactored packages

### Sprint 4: Polish (Haiku)

1. Precompile all regex patterns
2. Fix HTTP client timeout
3. Add typed YAML paths
4. Documentation updates

---

## Testing Considerations

After reorganization, update test structure:

```
pkg/
├── transform/
│   └── transform_test.go      # Move from cmd/transform_test.go
├── template/
│   └── template_test.go       # Move from cmd/template_test.go
├── chart/
│   └── chart_test.go          # New tests for chart operations
└── config/
    └── glob_test.go           # Move from cmd/glob_test.go

cmd/
├── integration_test.go        # Full CLI integration tests
└── cli_test.go                # Command-level tests
```

**Note:** During refactoring, run `go test ./...` after each extraction to ensure nothing breaks.

---

## Model Recommendations Summary

| Task Category              | Recommended Model | Rationale                                |
| -------------------------- | ----------------- | ---------------------------------------- |
| Package extraction         | **Sonnet**        | Clear boundaries, mechanical refactoring |
| Interface introduction     | **Sonnet**        | Type design decisions required           |
| Global state removal       | **Sonnet**        | Structural changes across files          |
| Simple fixes (sort, regex) | **Haiku**         | Localized, straightforward changes       |
| Error handling consistency | **Sonnet**        | Pattern application across codebase      |
| Documentation              | **Haiku**         | Simple additions                         |
| Complex algorithm changes  | **Opus**          | If any require deep analysis             |

Most tasks are well-suited for **Sonnet** as they involve clear refactoring patterns with defined boundaries. Use **Haiku** for quick, isolated fixes. Reserve **Opus** only if encountering complex algorithmic challenges or architectural decisions requiring deep trade-off analysis.
