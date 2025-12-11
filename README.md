# Helm list-to-map Plugin

A Helm plugin that converts array-based configurations to map-based configurations in Helm charts, enabling targeted value overrides.

## The Problem

Helm charts use YAML arrays for Kubernetes list fields (env vars, volumes, ports, etc.). Arrays are problematic for value overrides:

- Overriding a single item requires knowing its array index, which is fragile and changes when items are reordered
- Targeting an item by index (e.g., `--set env[1].value=foo`) breaks when the array order changes
- Overriding by recreating the entire array loses other default values
- Duplicate keys in arrays cause undefined Kubernetes behavior

## The Solution

Convert arrays with unique keys into maps, where the unique key becomes the map key:

### Values Transformation

`values.yaml` and custom values files:

```yaml
# Before (array)
env:
  - name: DB_HOST
    value: localhost

# After (map)
env:
  DB_HOST:
    value: localhost
```

This enables targeted overrides: `--set env.DB_HOST.value=production-host`

### Templates Transformation

Eg, `templates/deployment.yaml`:

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

## How It Works

The plugin automatically detects convertible fields by:

1. **Built-in K8s types**: Introspecting Kubernetes API types via the `k8s.io/api` Go packages, using `patchMergeKey` struct tags
2. **Custom Resources (CRDs)**: Loading CRD YAML definitions and extracting `x-kubernetes-list-type: map` and `x-kubernetes-list-map-keys` from the OpenAPI schema

For CRDs that are not automatically detected, you can:

- Use [`load-crd`](#helm-list-to-map-load-crd) to load CRD definitions from files or URLs
- Use [`add-rule`](#helm-list-to-map-add-rule) to manually define conversion rules

See [ARCHITECTURE.md](ARCHITECTURE.md) for design details.

## Requirements

- Helm 4.x
- Go 1.22+ (for building from source)

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

### `helm list-to-map`

```console
% helm list-to-map --help

The list-to-map plugin converts Helm chart array values with unique keys into
map-based configurations, making values easier to override and reducing duplication.

Usage:
  helm list-to-map [command] [flags]

Available Commands:
  detect      scan values.yaml and report convertible arrays
  convert     transform values.yaml and update templates
  load-crd    load CRD definitions for Custom Resource support
  list-crds   list loaded CRD types and their convertible fields
  add-rule    add a custom conversion rule to your config
  rules       list all active rules (built-in + custom)

Flags:
  -h, --help   help for list-to-map

Use "helm list-to-map [command] --help" for more information about a command.
```

### `helm list-to-map detect`

```console
% helm list-to-map detect --help

Scan a Helm chart to detect arrays that can be converted to maps based on
unique key fields. This is a read-only operation that reports potential conversions
without modifying any files.

Built-in Kubernetes types (Deployment, Pod, Service, etc.) are detected automatically.
For Custom Resources (CRs), first load their CRD definitions using 'helm list-to-map load-crd'.

Usage:
  helm list-to-map detect [flags]

Flags:
      --chart string    path to chart root (default: current directory)
      --config string   path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
  -h, --help            help for detect

Examples:
  # Detect convertible fields in a chart
  helm list-to-map detect --chart ./my-chart

  # First load CRDs for Custom Resources, then detect
  helm list-to-map load-crd https://raw.githubusercontent.com/.../alertmanager-crd.yaml
  helm list-to-map detect --chart ./my-chart
```

### `helm list-to-map convert`

```console
% helm list-to-map convert --help

Transform array-based configurations to map-based configurations in values.yaml
and automatically update corresponding template files. This command modifies files
in place, creating backups with the specified extension.

The conversion process:
  1. Scans templates using K8s API introspection and CRD schemas
  2. Identifies list fields with required unique keys (patchMergeKey or x-kubernetes-list-map-keys)
  3. Converts matching arrays to maps using unique key fields
  4. Updates template files to use new helper functions
  5. Generates helper templates if they don't exist

Built-in Kubernetes types are detected automatically. For Custom Resources (CRs),
first load their CRD definitions using 'helm list-to-map load-crd'.

Usage:
  helm list-to-map convert [flags]

Flags:
      --backup-ext string   backup file extension (default: ".bak")
      --chart string        path to chart root (default: current directory)
      --config string       path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
      --dry-run             preview changes without writing files
  -h, --help                help for convert

Examples:
  # Convert a chart with built-in K8s types
  helm list-to-map convert --chart ./my-chart

  # First load CRDs for Custom Resources, then convert
  helm list-to-map load-crd https://raw.githubusercontent.com/.../alertmanager-crd.yaml
  helm list-to-map convert --chart ./my-chart

  # Preview changes without modifying files
  helm list-to-map convert --dry-run
```

### `helm list-to-map load-crd`

```console
% helm list-to-map load-crd --help

Load CRD (Custom Resource Definition) files to enable detection of convertible
fields in Custom Resources. CRDs are stored in the plugin's config directory
and automatically loaded when running 'detect' or 'convert'.

The plugin extracts x-kubernetes-list-type and x-kubernetes-list-map-keys
annotations from the CRD's OpenAPI schema to identify convertible list fields.

Usage:
  helm list-to-map load-crd <source> [source...]

Arguments:
  source    CRD file path or URL (can specify multiple)

Flags:
  -h, --help   help for load-crd

Examples:
  # Load CRD from a local file
  helm list-to-map load-crd ./alertmanager-crd.yaml

  # Load CRD from a URL
  helm list-to-map load-crd https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_alertmanagers.yaml

  # Load multiple CRDs
  helm list-to-map load-crd ./crds/*.yaml
```

### `helm list-to-map list-crds`

```console
% helm list-to-map list-crds --help

List all loaded CRD types and their convertible fields.

Usage:
  helm list-to-map list-crds [flags]

Flags:
  -h, --help   help for list-crds
  -v           verbose - show all convertible fields for each CRD
```

### `helm list-to-map add-rule`

```console
% helm list-to-map add-rule --help

Add a custom conversion rule to your user configuration file.

Use this for:
  - CRDs that don't define x-kubernetes-list-map-keys in their OpenAPI schema
  - Custom resources without available CRD definitions
  - Any list field you want to convert that isn't auto-detected

Usage:
  helm list-to-map add-rule [flags]

Flags:
      --config string      path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
  -h, --help               help for add-rule
      --path string        dot path to array (end with []), e.g. database.primary.extraEnv[]
      --uniqueKey string   unique key field, e.g. name

Examples:
  helm list-to-map add-rule --path='istio.virtualService.http[]' --uniqueKey=name
  helm list-to-map add-rule --path='myapp.listeners[]' --uniqueKey=port
```

### `helm list-to-map rules`

```console
% helm list-to-map rules --help

List custom conversion rules for CRDs and custom resources.

Note: Built-in K8s types are detected automatically via API introspection
and do not require rules. Use 'detect' to see what will be converted.

Usage:
  helm list-to-map rules [flags]

Flags:
  -h, --help   help for rules
```
