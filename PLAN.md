# Migration Strategy Plan: Template-First Detection

## Problems Identified

### 1. Current Detection Limitation

- Plugin only detects arrays that have existing items with unique keys
- Empty arrays (`volumes: []`, `volumeMounts: []`) are invisible to content-based detection
- These empty arrays are common defaults in Helm charts, meant to be populated by users

### 2. Fundamental Design Issue

- Current approach: "Find data, analyze structure" (bottom-up)
- Needed approach: "Find template usage patterns, identify data locations" (top-down)

### 3. Incomplete Scope

- Plugin only converts chart's default `values.yaml`
- Doesn't handle users' custom values files (e.g., `prod-values.yaml`, `dev-values.yaml`)
- Doesn't preserve or add helpful comments explaining the new map structure

## Goals

### Primary Goal

Enable users to override individual items by key instead of replacing entire arrays.

Convert Helm charts from list-based to map-based patterns for fields with unique keys, which enables:

- Single-item overrides: Target specific items by their unique key (e.g., `--set env.DATABASE_PORT.value=3306`)
- Deep-merge capability: User overrides merge with defaults instead of replacing them
- Prevention of duplicate key issues: Map structure ensures uniqueness by design

### Secondary Goals

1. Detect conversion candidates by template analysis (not just values content)
2. Convert both populated and empty arrays
3. Support converting multiple values files (user custom configs)
4. Preserve or add documentation comments for converted structures

## Challenges

### Challenge 1: Template Pattern Recognition

Problem: Templates use various patterns for rendering lists:

```yaml
# Direct toYaml
{{- toYaml .Values.volumeMounts | nindent 12 }}

# With block
{{- with .Values.volumes }}
  {{- toYaml . | nindent 8 }}
{{- end }}

# Range iteration
{{- range .Values.env }}
  - name: {{ .name }}
    value: {{ .value }}
{{- end }}
```

Solution: Scan templates for these patterns using regex, extract value paths, match against configured rules.

**Critical Architectural Note**: The helper template architecture is the entire conceit of this plugin:

Before conversion (list in values.yaml):

```yaml
env:
  - name: HELLO
    value: world
  - name: FOO
    value: bar
```

After conversion (map in values.yaml - unique key removed from values):

```yaml
env:
  HELLO:
    value: world
  FOO:
    value: bar
```

Helper template (reconstructs K8s list format by injecting key back):

```yaml
{{- define "chart.env.render" -}}
{{- if . }}
env:
{{- range $name, $spec := . }}
  - name: {{ $name | quote }}
{{ toYaml $spec | nindent 4 }}
{{- end }}
{{- end }}
{{- end -}}
```

The map stores values WITHOUT the unique key field. Helper templates iterate the map and INJECT the key back as a field, producing the K8s list format that the API expects. This enables single-item overrides via `--set env.DATABASE_URL.value=...` while maintaining API compatibility.

**Range Pattern Disambiguation**: When scanning templates, we must distinguish:

- `range` over values we should convert (e.g., `{{- range .Values.env }}` where items have unique keys)
- `range` over values we should NOT convert (e.g., iteration without unique keys)

Strategy: Only flag a range pattern as convertible if the path matches a configured rule (built-in or custom).

### Challenge 2: Empty Array Conversion

Problem: `volumes: []` → `volumes: {}` looks trivial but requires:

- Knowing the field should be converted (from template analysis)
- Preserving YAML formatting and comments
- Updating templates even when no data exists

Solution: Template-first detection provides the "should convert" signal independent of data presence.

### Challenge 3: Multiple Values Files

Problem: Users have custom values files (`-f prod-values.yaml`) that also need conversion.

Solution: Add `--values-files` flag to specify additional files to convert alongside chart's `values.yaml`.

### Challenge 4: Comment Handling

Problem: YAML libraries typically strip comments. Options:

- Attempt to preserve/migrate existing comments (complex, error-prone)
- Generate new standard comments explaining map structure (reliable, consistent)

Solution: Generate standardized comments for converted fields showing example map structure.

## Proposed Solutions

### Solution 1: Template-First Detection (Core Change)

Implementation:

1. Scan `templates/*.yaml` for list-rendering patterns
2. Extract value paths (e.g., `.Values.volumeMounts`, `.Values.database.env`)
3. Match paths against configured rules
4. Build detection list from template analysis
5. Apply to values.yaml regardless of whether arrays are empty or populated

Changes:

- New function: `scanTemplatesForListUsage(chartRoot) []PathInfo`
- Modify `detect` command to run template scan first
- Use template-detected paths + content-detected paths (union)

Example Output:

```
Detected convertible arrays:
· volumeMounts → key=name, renderer=volumeMounts (found in templates)
· volumes → key=name, renderer=volumes (found in templates)
· database.env → key=name, renderer=env (found in values)
```

### Solution 2: Multi-File Conversion Support

Implementation:
Add `--values-files` flag to `convert` command:

```bash
helm list-to-map convert --chart ./my-chart \
  --values-files=prod-values.yaml,dev-values.yaml
```

Behavior:

- Always converts chart's `values.yaml`
- Optionally converts specified custom values files
- All files get same transformations based on template analysis

Note: Implement after template-first detection is working. Should be trivial once base functionality works with chart's values.yaml.

### Solution 3: K8s API Validation & Comment Generation

Implementation:
When converting a field like `volumeMounts: []` → `volumeMounts: {}`, validate the unique key is required in the K8s API, then add comment showing example.

**Phase 3A: API Type Validation (NEW - Required before conversion)**

For built-in renderers, use K8s API introspection to validate:

1. **Type Mapping**: Map renderer names to K8s API types:

   ```go
   "env" → corev1.EnvVar
   "volumeMounts" → corev1.VolumeMount
   "volumes" → corev1.Volume
   "ports" → corev1.ContainerPort
   ```

2. **Unique Key Validation**: Check if unique key field is **required** (not optional):

   ```go
   func isRequiredField(apiType reflect.Type, fieldName string) bool {
     field, found := apiType.FieldByName(fieldName)
     if !found {
       return false
     }
     jsonTag := field.Tag.Get("json")
     // Required if: no omitempty tag AND not a pointer type
     return !strings.Contains(jsonTag, "omitempty") && field.Type.Kind() != reflect.Ptr
   }
   ```

3. **Decision Logic**:
   - ✅ Convert if unique key is **required** in API
   - ❌ Skip if unique key is **optional** (has `omitempty` or is pointer)
   - ⚠️ Warn user if they configure optional key in custom rules

**Examples:**

- `EnvVar.name` → Required (no `omitempty`, not pointer) → ✅ Convert
- `ContainerPort.name` → Optional (`name,omitempty`) → ❌ Skip
- `HTTPRouteRule.name` → Optional (`*v1.SectionName`) → ❌ Skip

**Phase 3B: Comment Generation**

Strategy:

Use `gopkg.in/yaml.v3` which preserves comments and add standardized comment blocks above converted fields.

**For Built-in Kubernetes Types** (env, volumeMounts, volumes, containers):

- Introspect the Kubernetes API definitions using Go's `k8s.io/api` packages
- Include the full API type name (e.g., `corev1.VolumeMount`)
- Link to official Kubernetes API documentation
- Generate example structure from the actual API schema
- Show realistic field names and types from the K8s object spec
- Remove the unique key field from the example (e.g., show VolumeMount without `name`)

Example for `volumeMounts` using `corev1.VolumeMount`:

```go
// Use reflect.TypeOf(corev1.VolumeMount{}).PkgPath() and .Name() to get:
// Package: k8s.io/api/core/v1
// Type: VolumeMount

// Generate comment:
# volumeMounts: Map of volume mounts keyed by 'name'.
# API Type: corev1.VolumeMount
# Docs: https://kubernetes.io/docs/reference/kubernetes-api/config-and-storage-resources/volume/#VolumeMount
# Example structure (without 'name' field):
#   my-data:
#     mountPath: /data
#     readOnly: false
#     subPath: ""
#     mountPropagation: None
```

The API package provides:

- Type names via reflection: `reflect.TypeOf(corev1.VolumeMount{}).Name()` → "VolumeMount"
- Package paths: `reflect.TypeOf(corev1.VolumeMount{}).PkgPath()` → "k8s.io/api/core/v1"
- Struct field tags for JSON/YAML names and omitempty behavior
- Field required/optional status via `omitempty` tag and pointer types
- We can construct docs.k8s.io URLs from the API group and resource name

**For Custom CRD Rules** (user-added via `add-rule`):

- Cannot introspect schema since CRDs are not available statically
- Add helpful metadata from the rule itself:
  - Which template(s) use this field
  - The unique key field name
  - The renderer type (if applicable)

Example for custom rule:

```yaml
# my-custom-list: Map keyed by 'id' (from user rule)
# Used in: templates/custom-resource.yaml
# Renderer: generic
my-custom-list: {}
```

This dual approach ensures:

- Built-in types get accurate, schema-based documentation
- Custom CRD rules get helpful context even without schema knowledge
- Don't attempt to migrate existing comments (too fragile)

## Implementation Priority

### Phase 1: Template-First Detection (CRITICAL)

- [ ] Template Scanner
  - [ ] Implement regex patterns for detecting list-rendering in templates
  - [ ] Extract `.Values.*` paths from matches
  - [ ] Match extracted paths against configured rules
  - [ ] Return `[]PathInfo` for template-detected conversions

- [ ] Update Detection Logic
  - [ ] Run template scan before content analysis
  - [ ] Combine template-detected + content-detected paths (union)
  - [ ] Mark detection source (template vs values) in output

- [ ] Empty Array Handling
  - [ ] Detect empty `[]` arrays at template-identified paths
  - [ ] Convert `[]` → `{}` for matched fields
  - [ ] Ensure template rewriting works with empty values

### Phase 2: K8s API Validation (CRITICAL)

- [ ] API Type Infrastructure
  - [ ] Add `k8s.io/api/core/v1` dependency
  - [ ] Create type-to-struct mapping for built-in renderers
  - [ ] Implement `getAPIType(renderer) reflect.Type` function
  - [ ] Implement `isRequiredField(apiType, fieldName) bool` validation

- [ ] Update Built-in Rules with Validation
  - [ ] Validate `env` → `corev1.EnvVar.Name` is required
  - [ ] Validate `volumeMounts` → `corev1.VolumeMount.Name` is required
  - [ ] Validate `volumes` → `corev1.Volume.Name` is required
  - [ ] Check `ports` → `corev1.ContainerPort.Name` (may be optional!)
  - [ ] Filter out rules where unique key is optional

- [ ] Update Detection & Conversion Logic
  - [ ] Skip conversion if unique key is optional in K8s API
  - [ ] Add warning messages for skipped optional keys
  - [ ] Update rule matching to check API validation

### Phase 3: Comment Generation (DEFERRED to after validation)

- [ ] Comment Infrastructure
  - [ ] Switch to yaml.v3 with comment preservation
  - [ ] Generate comment blocks for converted fields
  - [ ] Test comment preservation through conversion

- [ ] K8s API Schema Integration (Built-in Types)
  - [ ] Use reflection to extract API type names and package paths
  - [ ] Construct kubernetes.io documentation URLs from type metadata
  - [ ] Generate example YAML from API struct definitions via reflection
  - [ ] Remove unique key field from generated examples

- [ ] Custom Rule Metadata (CRD Support)
  - [ ] Extract template usage information from scan results
  - [ ] Include unique key and renderer in comments
  - [ ] Format as helpful metadata block for user reference

### Phase 4: Multi-File Values Support (DEFERRED)

- [ ] Add `--values-files` flag to `convert` command
- [ ] Apply same transformations to all specified files
- [ ] Handle missing files gracefully

### Phase 5: Discovery Features (DEFERRED)

- Revisit auto-discovery of values files
- Consider additional UX improvements
- Evaluate need for legacy compatibility mode

## Questions Answered

1. Priority: ✅ Template-first detection before multi-file support
2. Comment Detail: ✅ Show example of supported map structure
3. Values File Discovery: ⏸️ Revisit after core functionality works
4. Backward Compatibility: ⏸️ Not needed initially, revisit if issues arise

## Success Criteria

### Phase 1 Complete When:

- `helm list-to-map detect --chart example` finds `volumeMounts` and `volumes` despite being empty
- Conversion correctly transforms `[]` → `{}` for detected fields
- Templates are updated to use new helper functions
- Helper templates are generated for map rendering

### Phase 2 Complete When:

- Converted fields have helpful comments with examples
- Comments survive round-trip through conversion
- Comment format is consistent and readable

### Phase 3 Complete When:

- Can specify custom values files with `--values-files` flag
- All specified files receive same transformations
- Works seamlessly with Phase 1 template detection
