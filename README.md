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

The plugin automatically detects convertible fields by introspecting Kubernetes API types via the `k8s.io/api` Go packages. For CRDs and custom resources, you can add rules manually with [`add-rule`](#helm-list-to-map-add-rule). See [ARCHITECTURE.md](ARCHITECTURE.md) for design details.

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

Usage:
  helm list-to-map detect [flags]

Flags:
      --chart string    path to chart root (default: current directory)
      --config string   path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
  -h, --help            help for detect
```

### `helm list-to-map convert`

```console
% helm list-to-map convert --help

Transform array-based configurations to map-based configurations in values.yaml
and automatically update corresponding template files. This command modifies files
in place, creating backups with the specified extension.

The conversion process:
  1. Scans templates using K8s API introspection
  2. Identifies list fields with required unique keys
  3. Converts matching arrays to maps using unique key fields
  4. Updates template files to use new helper functions
  5. Generates helper templates if they don't exist

Usage:
  helm list-to-map convert [flags]

Flags:
      --backup-ext string   backup file extension (default: ".bak")
      --chart string        path to chart root (default: current directory)
      --config string       path to user config (default: $HELM_CONFIG_HOME/list-to-map/config.yaml)
      --dry-run             preview changes without writing files
  -h, --help                help for convert
```

### `helm list-to-map add-rule`

```console
% helm list-to-map add-rule --help

Add a custom conversion rule to your user configuration file. Use this for CRDs
and custom resources that cannot be introspected via K8s API.

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
