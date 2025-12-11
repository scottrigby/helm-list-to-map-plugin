#!/usr/bin/env bash
# Updates the Usage section of README.md with current help output
#
# Usage: ./scripts/update-readme-usage.sh
#
# Requires the plugin to be installed: helm plugin install .
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
README="$ROOT_DIR/README.md"

if ! command -v helm &> /dev/null; then
    echo "Error: helm not found in PATH" >&2
    exit 1
fi

if ! helm list-to-map --help &> /dev/null; then
    echo "Error: list-to-map plugin not installed. Run 'helm plugin install .' first." >&2
    exit 1
fi

# Generate the usage section
generate_usage() {
    echo '## Usage'
    echo ''

    # Top-level command
    echo '### `helm list-to-map`'
    echo ''
    echo '```console'
    echo '% helm list-to-map --help'
    helm list-to-map --help
    echo '```'

    # Subcommands (extracted dynamically from help output)
    subcommands=$(helm list-to-map --help | awk '/^Available Commands:/,/^$/' | tail -n +2 | awk '{print $1}' | grep -v '^$')
    for cmd in $subcommands; do
        echo ''
        echo "### \`helm list-to-map $cmd\`"
        echo ''
        echo '```console'
        echo "% helm list-to-map $cmd --help"
        helm list-to-map "$cmd" --help
        echo '```'
    done
}

# Extract everything before ## Usage
before_usage=$(sed -n '1,/^## Usage$/p' "$README" | sed '$d')

# Extract everything after the Usage section (next ## heading or EOF)
after_usage=$(awk '
    /^## Usage$/ { in_usage=1; next }
    in_usage && /^## / { in_usage=0 }
    !in_usage && found_end { print }
    in_usage && /^## / { found_end=1; print }
' "$README")

# Combine
{
    echo "$before_usage"
    echo ""
    generate_usage
    printf '%s' "$after_usage"
} > "$README.tmp"

mv "$README.tmp" "$README"
echo "Updated $README"
