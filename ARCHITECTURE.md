# Architecture

This document explains the design decisions behind the list-to-map Helm plugin.

For the problem statement, solution overview, installation, and usage, see [README.md](README.md).

## Overview

The plugin uses Kubernetes API introspection to automatically detect convertible fields:

1. **Parses templates** to find `apiVersion` and `kind` of K8s resources being constructed
2. **Maps to K8s Go types** (e.g., `apps/v1` + `Deployment` → `appsv1.Deployment`)
3. **Reads [`patchMergeKey`](#the-patchmergekey-discovery) struct tags** from K8s API types to determine the unique key for each list field
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

Kubernetes uses Strategic Merge Patch for applying changes to resources. The K8s API Go structs contain `patchMergeKey` struct tags that define which field uniquely identifies items in a list:

```go
// From k8s.io/api/core/v1/types.go
type PodSpec struct {
    Volumes []Volume `json:"volumes,omitempty" patchMergeKey:"name" ...`
    Containers []Container `json:"containers" patchMergeKey:"name" ...`
}

type Container struct {
    VolumeMounts []VolumeMount `json:"volumeMounts,omitempty" patchMergeKey:"mountPath" ...`
    Ports []ContainerPort `json:"ports,omitempty" patchMergeKey:"containerPort" ...`
}
```

This is the definitive source for unique keys. Examples:

- `volumes` → keyed by `name`
- `containers` → keyed by `name`
- `volumeMounts` → keyed by `mountPath` (not `name`!)
- `ports` → keyed by `containerPort` (not `name`!)

Using `patchMergeKey` ensures the plugin uses the same uniqueness semantics as Kubernetes itself.

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

## User Rules for CRDs

Custom Resource Definitions (CRDs) are not part of the K8s API packages, so the plugin cannot introspect them. Users can define rules manually. Example:

```bash
helm list-to-map add-rule --path='istio.virtualService.http[]' --uniqueKey=name
```

User rules are stored in `$HELM_CONFIG_HOME/list-to-map/config.yaml` and supplement the automatic detection.

## File Overview

- `cmd/analyzer.go`: K8s type registry and field schema navigation using reflection
- `cmd/parser.go`: Helm template parsing and directive extraction
- `cmd/main.go`: CLI commands, value migration, and template rewriting

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
