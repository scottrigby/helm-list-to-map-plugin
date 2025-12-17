# Contributing

Contributions are welcome! Feel free to open an issue or pull request.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).

## Development

```bash
# Build (outputs to bin/list-to-map)
make build

# Run all tests (unit + cmd + integration + e2e)
make test

# Run only fast tests (unit + cmd - recommended during development)
make test-no-e2e

# Run tests by type
make test-unit          # Unit tests only (pkg/) - fastest
make test-cmd           # In-process CLI tests (cmd/) - fast
make test-integration   # Integration tests - medium speed
make test-e2e           # E2E tests (binary execution) - slowest

# Run tests with coverage
make test-cover

# Lint
make lint

# Format code
make fmt
```

## Test Organization

Tests are organized by execution mode and purpose:

- **`pkg/`** - Unit tests (fast, pure functions with mocks)
- **`cmd/`** - In-process CLI tests (fast, tests command behavior without binary execution)
- **`integration/`** - Integration tests (medium, cross-package workflows, requires `//go:build integration` tag)
- **`e2e/`** - End-to-end tests (slow, binary execution smoke tests, requires `//go:build e2e` tag)

### Writing Tests

**Unit Tests (pkg/):**

- Test pure functions with mocked dependencies
- Fast, focused on single-package logic
- Example: `pkg/transform/transform_test.go`

**In-Process CLI Tests (cmd/):**

- Test command behavior by calling `runDetect()`, `runConvert()`, etc. directly
- Fast, no binary compilation required
- Use `internal/testutil` for test setup
- Example: `cmd/detect_test.go`, `cmd/convert_test.go`

**Integration Tests (integration/):**

- Test cross-package workflows (e.g., transform + YAML parsing)
- Require `//go:build integration` tag
- Use `integration/testutil` for chart fixtures
- Example: `integration/comment_strip_test.go`

**E2E Tests (e2e/):**

- Minimal smoke tests that verify binary builds and runs
- Require `//go:build e2e` tag
- Use `e2e/testutil` for binary building
- Example: `e2e/smoke_test.go`

### Running Tests During Development

For fastest feedback during development:

```bash
# Run only unit + cmd tests (skips slow E2E tests)
make test-no-e2e
```

Before pushing changes:

```bash
# Run all tests including integration and E2E
make test
```

## Updating Documentation

After changing CLI help text, update the README:

```bash
./scripts/update-readme-usage.sh
```

This requires the plugin to be installed (`helm plugin install /path/to/plugin/dir`).

## Roadmap

See [ROADMAP.md](ROADMAP.md) for planned enhancements and their status. Contributions welcome!
