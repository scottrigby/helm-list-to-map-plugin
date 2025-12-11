# list-to-map

A Helm plugin that converts array-based configurations to map-based configurations in Helm charts, enabling targeted value overrides.

## The Problem

Helm charts define arrays for Kubernetes list fields:

```yaml
# values.yaml
env:
  - name: DATABASE_HOST
    value: postgres.default.svc
  - name: DATABASE_PORT
    value: "5432"
```

This creates problems:

1. **Overriding is painful**: To change just `DATABASE_PORT`, you must recreate the entire array or use fragile index-based overrides like `--set env[1].value=3306`
2. **Duplicate keys cause issues**: Accidentally defining `DATABASE_HOST` twice leads to undefined Kubernetes behavior

## The Solution

Convert arrays with unique keys into maps:

```yaml
# values.yaml (after conversion)
env:
  DATABASE_HOST:
    value: postgres.default.svc
  DATABASE_PORT:
    value: "5432"
```

Now override individual values:

```bash
helm upgrade myapp ./chart --set env.DATABASE_PORT.value=3306
```

## How It Works

The plugin uses Kubernetes API introspection to automatically detect convertible fields:

1. **Parses templates** to find `apiVersion` and `kind` of K8s resources being constructed
2. **Maps to K8s Go types** (e.g., `apps/v1` + `Deployment` → `appsv1.Deployment`)
3. **Reads `patchMergeKey` struct tags** from K8s API types to determine the unique key for each list field
4. **Converts values and templates** using a single generic helper

The `patchMergeKey` is the same mechanism Kubernetes uses for Strategic Merge Patch, ensuring the plugin uses correct uniqueness semantics. Examples:

| Field              | patchMergeKey   |
| ------------------ | --------------- |
| `volumes`          | `name`          |
| `containers`       | `name`          |
| `volumeMounts`     | `mountPath`     |
| `ports`            | `containerPort` |
| `env`              | `name`          |
| `imagePullSecrets` | `name`          |

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed design rationale.

## Installation

```bash
helm plugin install https://github.com/yourorg/list-to-map
```

Or build from source:

```bash
git clone https://github.com/yourorg/list-to-map
cd list-to-map
make build
helm plugin install .
```

## Usage

### Detect convertible arrays

```bash
helm list-to-map detect --chart ./my-chart
```

Example output:

```
Detected convertible arrays:
· env → key=name, type=corev1.EnvVar
· volumes → key=name, type=corev1.Volume
· imagePullSecrets → key=name, type=corev1.LocalObjectReference
```

### Preview changes

```bash
helm list-to-map convert --chart ./my-chart --dry-run
```

### Convert chart

```bash
helm list-to-map convert --chart ./my-chart
```

This will:

- Convert arrays to maps in `values.yaml`
- Update templates to use the generic helper
- Generate `_listmap.tpl` helper template
- Create `.bak` backups of modified files

### Custom rules for CRDs

CRDs cannot be introspected automatically. Add rules manually. Example:

```bash
helm list-to-map add-rule \
  --path='istio.virtualService.http[]' \
  --uniqueKey=name
```

Rules are stored in `$HELM_CONFIG_HOME/list-to-map/config.yaml`.

## Commands

| Command    | Description                                |
| ---------- | ------------------------------------------ |
| `detect`   | Scan chart and report convertible arrays   |
| `convert`  | Transform values.yaml and update templates |
| `add-rule` | Add a custom conversion rule for CRDs      |
| `rules`    | List all active rules                      |

### Common flags

- `--chart` - Path to chart root (default: current directory)
- `--config` - Path to config file (default: `$HELM_CONFIG_HOME/list-to-map/config.yaml`)

### convert flags

- `--dry-run` - Preview changes without writing files
- `--backup-ext` - Backup file extension (default: `.bak`)

### add-rule flags

- `--path` - Dot path to array with `[]` suffix (e.g., `myapp.items[]`)
- `--uniqueKey` - Field name to use as map key

## Template Transformation

Before:

```yaml
{{- with .Values.volumes }}
volumes:
  {{- toYaml . | nindent 8 }}
{{- end }}
```

After:

```yaml
{
  {
    - include "chart.listmap.render" (dict "items" (index .Values "volumes") "key" "name" "section" "volumes"),
  },
}
```

The helper template iterates the map and reconstructs the K8s list format.

## Requirements

- Helm 3.x
- Go 1.22+ (for building from source)

## TODO

- [ ] Setup release process with GoReleaser
- [ ] Add GitHub Actions workflow
- [ ] Convert to WebAssembly for cross-platform distribution

## License

[Add your license here]
