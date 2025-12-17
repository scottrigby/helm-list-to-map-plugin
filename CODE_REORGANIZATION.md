# Code Reorganization Plan

This document analyzes the current codebase structure and proposes improvements for readability, maintainability, and idiomatic Go patterns.

---

## Progress Tracker

> **Note:** Phases were reordered from the original plan based on implementation experience.
> Original Phase 2 (eliminate global state) is now Phase 5. Phase 2 below was added
> as a necessary intermediate step.

| Phase   | Status         | Description                                                               |
| ------- | -------------- | ------------------------------------------------------------------------- |
| Phase 1 | ✅ Complete    | Extract core packages (pkg/transform, pkg/template, pkg/detect)           |
| Phase 2 | ✅ Complete    | Wire cmd/ to use new packages, remove duplicate code (~800 lines removed) |
| Phase 3 | 🔲 Not Started | Split cmd/main.go into command files (1:1 mapping)                        |
| Phase 4 | 🔲 Not Started | Move analyzer.go → pkg/k8s/, crd.go → pkg/crd/                            |
| Phase 5 | 🔲 Not Started | Options structs, App context, eliminate global state                      |
| Phase 6 | 🔲 Not Started | Interfaces for testability                                                |

---

## Current State Analysis

### File Structure Overview (Updated)

| File              | Lines | Purpose                                                |
| ----------------- | ----- | ------------------------------------------------------ |
| `cmd/main.go`     | ~1900 | CLI framework, commands, helpers (reduced from 2693)   |
| `cmd/analyzer.go` | 819   | K8s type introspection, conversion candidate detection |
| `cmd/crd.go`      | 724   | CRD registry, loading, metadata extraction             |
| `cmd/parser.go`   | 435   | Template parsing, directive extraction                 |
| `pkg/transform/`  | ✅    | Array-to-map transformation (extracted)                |
| `pkg/template/`   | ✅    | Template rewriting, helper generation (extracted)      |
| `pkg/detect/`     | ✅    | Shared types (DetectedCandidate)                       |

### Remaining Issues

#### 1. Monolithic main.go (~1900 lines)

Still contains 8 command implementations that should be split:

- `runDetect`, `runRecursiveDetect`
- `runConvert`, `runRecursiveConvert`
- `runLoadCRD`, `runListCRDs`
- `runAddRule`, `runListRules`

#### 2. Global State

```go
// main.go - Package-level mutable state (still present)
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
```

#### 3. Domain Logic in cmd/

`analyzer.go` and `crd.go` contain domain logic (K8s introspection, CRD handling) that should be in `pkg/` for reusability and testability.

---

## Phase 3: Split Command Files (Next)

**Recommended Model: Sonnet** - Mechanical file splitting

### Target Structure

Split `cmd/main.go` into one file per command (1:1 mapping for discoverability):

```
cmd/
├── root.go           # main(), usage(), command routing (~100 lines)
├── detect.go         # runDetect, runRecursiveDetect
├── convert.go        # runConvert, runRecursiveConvert, updateUmbrellaValues
├── load_crd.go       # runLoadCRD
├── list_crds.go      # runListCRDs
├── add_rule.go       # runAddRule
├── list_rules.go     # runListRules
├── helpers.go        # findChartRoot, loadValuesNode, backupFile, matchRule, matchGlob
├── analyzer.go       # (keep for now, move in Phase 4)
├── crd.go            # (keep for now, move in Phase 4)
└── parser.go         # (keep as-is)
```

### Rationale for 1:1 Mapping

1. **Discoverability** - `list-to-map list-crds` → look in `list_crds.go`
2. **Future growth** - Each command has room to expand
3. **Cobra convention** - Matches industry standard if we migrate later
4. **Small files are OK** - A focused 50-line file is perfectly acceptable

### Implementation Steps

1. Create `root.go` with `main()`, `usage()`, and command switch
2. Create each command file, moving the `run*` function and its helpers
3. Create `helpers.go` with shared utilities
4. Ensure all tests still pass
5. Run `goimports` to fix imports

### Global Variables Handling

Keep global variables in `root.go` for now (Phase 5 will convert to Options structs).

---

## Phase 4: Extract Domain Packages

**Recommended Model: Sonnet**

### Move analyzer.go → pkg/k8s/

```
pkg/k8s/
├── types.go      # kubeTypeRegistry, resolveKubeAPIType()
├── fields.go     # navigateFieldSchema(), findFieldByJSONTag()
├── merge.go      # getMergeKeyFromStrategicPatch()
└── detect.go     # detectConversionCandidates() - core logic
```

### Move crd.go → pkg/crd/

```
pkg/crd/
├── registry.go   # CRDRegistry struct, NewCRDRegistry()
├── load.go       # LoadFromFile(), LoadFromURL(), LoadFromDirectory()
├── types.go      # k8sTypeSignature, CRDFieldInfo
└── embedded.go   # Embedded type detection logic
```

### What Stays in cmd/

After Phase 4, cmd/ should only contain:

- CLI entry point and routing
- Flag parsing
- Output formatting
- Error handling for CLI context

---

## Phase 5: Options Structs & App Context

**Recommended Model: Sonnet**

### Create options.go

```go
// cmd/options.go
package main

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

type LoadCRDOptions struct {
    Source string // file path, URL, or directory
}

type AddRuleOptions struct {
    PathPattern string
    UniqueKey   string
    Renderer    string
}
```

### Update Commands to Use Options

```go
// Before (global state)
func runDetect() {
    root, err := findChartRoot(chartDir)
    // uses global: recursive, configPath
}

// After (explicit options)
func runDetect(opts DetectOptions) error {
    root, err := findChartRoot(opts.ChartDir)
    // all state from opts
}
```

### App Context (Optional)

If dependency injection is needed:

```go
// cmd/app.go
type App struct {
    CRDRegistry *crd.Registry
    Config      *config.Config
    Stdout      io.Writer
    Stderr      io.Writer
}
```

---

## Phase 6: Interfaces for Testability

**Recommended Model: Sonnet**

### FileSystem Interface

```go
// pkg/fs/fs.go
type FileSystem interface {
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, data []byte, perm os.FileMode) error
    Stat(path string) (os.FileInfo, error)
    WalkDir(root string, fn fs.WalkDirFunc) error
}
```

### CRD Registry Interface

```go
// pkg/crd/interface.go
type Registry interface {
    LoadFromFile(path string) error
    LoadFromURL(url string) error
    HasType(apiVersion, kind string) bool
    GetFieldInfo(apiVersion, kind, yamlPath string) *FieldInfo
    ListTypes() []string
}
```

---

## Quick Wins (Can Do Anytime)

These are independent improvements that can be done in any phase:

| Task                             | Model | Effort | File                    |
| -------------------------------- | ----- | ------ | ----------------------- |
| Precompile regex patterns        | Haiku | Low    | pkg/template/rewrite.go |
| HTTP client timeout              | Haiku | Low    | cmd/crd.go              |
| Document Helm-specific functions | Haiku | Low    | pkg/template/helper.go  |

---

## Test Data Location

**Recommendation: Keep `cmd/testdata/` in place**

The testdata contains CLI integration test fixtures:

- `charts/` - Full Helm chart fixtures
- `crds/` - CRD YAML fixtures
- `golden/` - Expected output files
- `values/` - Values.yaml edge cases

These test the CLI end-to-end, not individual packages. The `pkg/` tests use inline test cases (appropriate for unit tests).

---

## Implementation Notes for Sonnet

### For Phase 3 (Command File Split)

1. Start with `root.go` - extract `main()`, `usage()`, and the command switch
2. Move one command at a time, running tests after each
3. Use `goimports -w cmd/` after each file creation
4. Keep the PR focused - just file splitting, no logic changes

### For Phase 4 (Domain Extraction)

1. Create the package structure first
2. Move types before functions (to avoid circular imports)
3. Export functions that cmd/ needs (capitalize first letter)
4. Update imports in cmd/
5. Consider temporary type aliases for smooth transition

### Commands to Verify

```bash
# After each change:
go build ./...
go test ./...
golangci-lint run
```

---

## Model Recommendations Summary

| Phase                     | Recommended Model | Rationale             |
| ------------------------- | ----------------- | --------------------- |
| Phase 3: Split commands   | **Sonnet**        | Mechanical file moves |
| Phase 4: Extract packages | **Sonnet**        | Clear boundaries      |
| Phase 5: Options structs  | **Sonnet**        | Pattern application   |
| Phase 6: Interfaces       | **Sonnet**        | Type design           |
| Quick wins                | **Haiku**         | Localized changes     |
| Architecture questions    | **Opus**          | Trade-off analysis    |
