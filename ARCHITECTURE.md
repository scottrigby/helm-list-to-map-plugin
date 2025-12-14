# Architecture

This document explains the design decisions behind the list-to-map Helm plugin.

For the problem statement, solution overview, installation, and usage, see [README.md](README.md).

## Overview

The plugin uses Kubernetes API introspection and CRD schema parsing to automatically detect convertible fields:

1. **Parses templates** to find `apiVersion` and `kind` of K8s resources being constructed
2. **Maps to K8s Go types** (e.g., `apps/v1` + `Deployment` → `appsv1.Deployment`) or looks up loaded CRD schemas
3. **Reads [`patchMergeKey`](#the-patchmergekey-discovery) struct tags** from K8s API types, or [`x-kubernetes-list-map-keys`](#crd-schema-parsing) from CRD OpenAPI schemas
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

Currently detected embedded types:

| Type                     | Merge Key       | Common Field Names         |
| ------------------------ | --------------- | -------------------------- |
| Container                | `name`          | containers, initContainers |
| Volume                   | `name`          | volumes                    |
| VolumeMount              | `mountPath`     | volumeMounts               |
| EnvVar                   | `name`          | env                        |
| ContainerPort            | `containerPort` | ports                      |
| Toleration               | `key`           | tolerations                |
| TopologySpreadConstraint | `topologyKey`   | topologySpreadConstraints  |
| HostAlias                | `ip`            | hostAliases                |
| VolumeDevice             | `devicePath`    | volumeDevices              |
| ResourceClaim            | `name`          | claims                     |
| LocalObjectReference     | `name`          | imagePullSecrets           |

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

## File Overview

- `cmd/analyzer.go`: K8s type registry, field schema navigation using reflection, and detection result types
- `cmd/crd.go`: CRD YAML parsing, registry for Custom Resource support, and embedded K8s type detection
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
