# Contributing

Contributions are welcome! Feel free to open an issue or pull request.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).

## Development

```bash
# Build (outputs to bin/list-to-map)
make build

# Run tests
go test -v ./...

# Lint
golangci-lint run
```

## Updating Documentation

After changing CLI help text, update the README:

```bash
./scripts/update-readme-usage.sh
```

This requires the plugin to be installed (`helm plugin install /path/to/plugin/dir`).

## Roadmap

See [ROADMAP.md](ROADMAP.md) for planned enhancements and their status. Contributions welcome!
