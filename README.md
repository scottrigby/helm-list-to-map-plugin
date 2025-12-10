# list-to-map

A Helm plugin that automatically converts array-based configurations to map-based configurations in your Helm charts, making values easier to override and reducing duplication in Kubernetes manifests.

## The Problem

When working with Helm charts, you often define arrays of objects in `values.yaml`:

```yaml
# values.yaml
database:
  env:
    - name: DATABASE_HOST
      value: postgres.default.svc
    - name: DATABASE_PORT
      value: "5432"
    - name: DATABASE_NAME
      value: myapp
```

This creates two problems:

1. **Overriding is painful**: To change just `DATABASE_PORT`, you must either recreate the entire array (losing other values) or use complex index-based overrides like `--set database.env[1].value=3306`.

2. **Duplicate keys cause confusion**: If you accidentally define `DATABASE_HOST` twice, Kubernetes accepts both entries, leading to undefined behavior.

## The Solution

This plugin converts arrays with unique keys into maps:

```yaml
# values.yaml (after conversion)
database:
  env:
    DATABASE_HOST:
      value: postgres.default.svc
    DATABASE_PORT:
      value: "5432"
    DATABASE_NAME:
      value: myapp
```

Now you can easily override values:

```bash
helm upgrade myapp ./chart --set database.env.DATABASE_PORT.value=3306
```

The plugin automatically updates your templates to render the new structure correctly.

## Installation

Install as a Helm plugin:

```bash
helm plugin install https://github.com/yourorg/list-to-map
```

Or for local development:

```bash
git clone https://github.com/yourorg/list-to-map
cd list-to-map
make build
helm plugin install .
```

## Usage

### 1. Detect convertible arrays

Scan your chart to see what can be converted:

```bash
helm list-to-map detect --chart ./my-chart
```

Available subcommands:

- `detect` - Scan values.yaml and report convertible arrays
- `convert` - Transform values.yaml and update templates
- `add-rule` - Add a custom conversion rule
- `rules` - List all active rules (built-in + custom)

Example output:

```
Detected convertible arrays:
· database.env → key=name, renderer=env
· web.volumeMounts → key=name, renderer=volumeMounts
· web.volumes → key=name, renderer=volumes
```

### 2. Preview changes

See what would change without modifying files:

```bash
helm list-to-map convert --chart ./my-chart --dry-run
```

### 3. Convert your chart

Apply the transformation:

```bash
helm list-to-map convert --chart ./my-chart
```

This will:

- ✅ Convert arrays to maps in `values.yaml`
- ✅ Update template files to use the new structure
- ✅ Generate helper template functions (`_env.tpl`, `_volumeMounts.tpl`, etc.)
- ✅ Create `.bak` backups of all modified files

## Built-in Support

The plugin has built-in rules for common Kubernetes patterns:

| Pattern               | Unique Key(s)                   | Example Path                |
| --------------------- | ------------------------------- | --------------------------- |
| Environment variables | `name`                          | `*.env[]`, `*.*.extraEnv[]` |
| Volume mounts         | `name`, `mountPath`             | `*.volumeMounts[]`          |
| Volumes               | `name`                          | `*.volumes[]`               |
| Ports                 | `name`, `containerPort`, `port` | `*.ports[]`                 |
| Containers            | `name`                          | `*.containers[]`            |
| Image pull secrets    | `name`                          | `*.imagePullSecrets[]`      |
| HTTP headers          | `name`                          | `*.httpGet.headers[]`       |

### Real-world Example: PostgreSQL Chart

Before conversion:

```yaml
# values.yaml
postgresql:
  primary:
    extraEnv:
      - name: POSTGRESQL_MAX_CONNECTIONS
        value: "100"
      - name: POSTGRESQL_SHARED_BUFFERS
        value: 256MB
      - name: POSTGRESQL_LOG_TIMEZONE
        value: UTC
```

After running `helm list-to-map convert`:

```yaml
# values.yaml
postgresql:
  primary:
    extraEnv:
      POSTGRESQL_MAX_CONNECTIONS:
        value: "100"
      POSTGRESQL_SHARED_BUFFERS:
        value: 256MB
      POSTGRESQL_LOG_TIMEZONE:
        value: UTC
```

Now override individual values easily:

```bash
helm upgrade postgres ./chart \
  --set postgresql.primary.extraEnv.POSTGRESQL_MAX_CONNECTIONS.value=200
```

## Custom Configuration

### When You Need Custom Rules

The built-in rules cover standard Kubernetes fields, but you may need custom configuration for:

1. **Custom CRDs**: Resources with array fields that have unique identifiers
2. **Non-standard paths**: Arrays at unusual locations in your values
3. **Different unique keys**: Arrays where the unique field isn't `name`

### Real-world Example: Istio VirtualService

Consider an Istio VirtualService with HTTP routes in your Helm chart:

```yaml
# values.yaml
istio:
  virtualService:
    http:
      - name: reviews-v2
        match:
          - uri:
              prefix: /v2
        route:
          - destination:
              host: reviews
              subset: v2
      - name: reviews-v1
        match:
          - uri:
              prefix: /
        route:
          - destination:
              host: reviews
              subset: v1
```

This pattern isn't covered by default rules. Add a custom rule:

```bash
helm list-to-map add-rule \
  --path='istio.virtualService.http[]' \
  --uniqueKey=name \
  --renderer=generic
```

This creates a configuration file at `$HELM_CONFIG_HOME/list-to-map/config.yaml`:

```yaml
rules:
  - pathPattern: istio.virtualService.http[]
    uniqueKeys:
      - name
    renderer: generic
```

Now running `helm list-to-map convert` will transform your VirtualService routes:

```yaml
# values.yaml (after conversion)
istio:
  virtualService:
    http:
      reviews-v2:
        match:
          - uri:
              prefix: /v2
        route:
          - destination:
              host: reviews
              subset: v2
      reviews-v1:
        match:
          - uri:
              prefix: /
        route:
          - destination:
              host: reviews
              subset: v1
```

### Viewing Active Rules

See all rules (built-in + custom):

```bash
helm list-to-map rules
```

### Path Pattern Syntax

Path patterns use glob-style matching:

- `*.env[]` - matches `database.env`, `web.env`, etc.
- `*.*.extraEnv[]` - matches `postgresql.primary.extraEnv`, `redis.replica.extraEnv`, etc.
- `istio.virtualService.http[]` - exact match only

The `[]` suffix indicates an array position.

## How It Works

1. **Detection**: Scans `values.yaml` for arrays matching configured patterns
2. **Key Selection**: Finds the best unique key (must be present in 50%+ of items)
3. **Conversion**: Transforms arrays to maps, using the key field as the map key
4. **Template Updates**: Rewrites Go templates to iterate over maps instead of arrays
5. **Helper Generation**: Creates helper templates to render the new structure

### Template Transformation

Before:

```yaml
# templates/deployment.yaml
env: { { - toYaml .Values.database.env | nindent 2 } }
```

After:

```yaml
# templates/deployment.yaml
{
  {
    - include "chart.env.render" (dict "env" (index .Values "database" "env")),
  },
}
```

The generated `_env.tpl` helper handles the iteration and rendering.

## Configuration Options

Edit `$HELM_CONFIG_HOME/list-to-map/config.yaml` (usually `~/.config/helm/list-to-map/config.yaml`):

```yaml
# Add custom rules
rules:
  - pathPattern: myapp.listeners[]
    uniqueKeys:
      - port # Try 'port' first
      - name # Fall back to 'name'
    renderer: generic

# Handle duplicates by keeping the last entry (default: true)
lastWinsDuplicates: true

# Sort map keys alphabetically (default: true)
sortKeys: true
```

## Command Reference

All commands support these common flags:

- `--chart` - Path to chart root (default: current directory)
- `--config` - Path to custom config file (default: `$HELM_CONFIG_HOME/list-to-map/config.yaml`)

### detect

Scan and report convertible arrays without making changes.

```bash
helm list-to-map detect --chart ./my-chart
```

### convert

Transform values.yaml and update templates.

```bash
# Dry run (preview only)
helm list-to-map convert --chart ./my-chart --dry-run

# Actually write changes
helm list-to-map convert --chart ./my-chart

# Custom backup extension
helm list-to-map convert --chart ./my-chart --backup-ext=.backup
```

Flags:

- `--dry-run` - Preview changes without writing files
- `--backup-ext` - Backup file extension (default: `.bak`)

### add-rule

Add a custom conversion rule to your user config.

```bash
helm list-to-map add-rule \
  --path='istio.virtualService.http[]' \
  --uniqueKey=name \
  --renderer=generic
```

Flags:

- `--path` - Dot path to array with `[]` suffix
- `--uniqueKey` - Field name to use as map key
- `--renderer` - Rendering hint: `env`, `volumeMounts`, `volumes`, `ports`, `generic`

### rules

List all active rules (built-in + custom).

```bash
helm list-to-map rules
```

## Renderers

The renderer determines how the map is converted back to YAML in templates:

- `env` - Environment variables with `name` field
- `volumeMounts` - Volume mounts with `name` field
- `volumes` - Volumes with `name` field
- `ports` - Container/service ports with `name` field
- `containers` - Container specs with `name` field
- `generic` - Generic key-value structures with `name` field

All renderers output sorted keys and properly formatted YAML.

## Backup and Recovery

The plugin creates `.bak` backups before modifying files:

```bash
# Restore from backup if needed
cp values.yaml.bak values.yaml
cp templates/deployment.yaml.bak templates/deployment.yaml
```

## Requirements

- Helm 3.x
- Go 1.22+ (for building from source, not required for plugin installation)

## TODO

- [ ] Setup release process with GoReleaser
  - [ ] Configure `.goreleaser.yml` for multi-platform builds
  - [ ] Update `plugin.yaml` hooks to download pre-built binaries from GitHub releases instead of building from source
  - [ ] Add release workflow (GitHub Actions)
- [ ] Convert plugin to WebAssembly
  - [ ] Compile Go to Wasm using TinyGo
  - [ ] Update plugin to use `TMPDIR` for Wasm execution
  - [ ] Test with Helm's Wasm plugin support

## Contributing

Contributions welcome! Please open an issue or pull request on GitHub.

## License

[Add your license here]

## Sources

- [Istio VirtualService Documentation](https://istio.io/latest/docs/reference/config/networking/virtual-service/)
- [ArgoCD Multiple Sources Documentation](https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/)
