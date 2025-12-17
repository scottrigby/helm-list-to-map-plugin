# Module/binary settings
BINARY := list-to-map
PKG := ./cmd

# Build output path
BIN_DIR := bin
BIN_PATH := $(BIN_DIR)/$(BINARY)

# Version via git tag or commit
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0")

# Test package paths
TESTPKG_UNIT := ./pkg/...
TESTPKG_CMD := ./cmd/...
TESTPKG_ALL := $(TESTPKG_CMD) $(TESTPKG_UNIT)

# Test flags
TESTFLAGS := -shuffle=on -count=1

# Default target
.PHONY: all
all: build

# Build for native platform
.PHONY: build
build:
	@echo "Building $(BINARY)..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(VERSION)" -trimpath -o $(BIN_PATH) $(PKG)
	@chmod +x $(BIN_PATH)

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -f $(BIN_PATH)
	@rm -f coverage*.out coverage*.html

# Verify module and dependencies
.PHONY: deps
deps:
	@go mod tidy
	@go mod verify

# Run all tests (unit + cmd + integration + e2e)
.PHONY: test
test: test-unit test-cmd test-integration test-e2e

# Run only unit tests (pkg/ - fast)
.PHONY: test-unit
test-unit:
	@echo
	@echo "==> Running unit tests (pkg/) <=="
	@go test -v $(TESTPKG_UNIT) $(TESTFLAGS)

# Run in-process CLI tests (cmd/ - fast)
.PHONY: test-cmd
test-cmd:
	@echo
	@echo "==> Running in-process CLI tests (cmd/) <=="
	@go test -v $(TESTPKG_CMD) $(TESTFLAGS)

# Run integration tests (cross-package - medium speed)
.PHONY: test-integration
test-integration:
	@echo
	@echo "==> Running integration tests <=="
	@go test -v -tags=integration ./integration/... $(TESTFLAGS)

# Run E2E tests (binary execution - slow)
.PHONY: test-e2e
test-e2e:
	@echo
	@echo "==> Running E2E tests (binary execution) <=="
	@go test -v -tags=e2e ./e2e/... $(TESTFLAGS)

# Run tests without binary execution (unit + cmd + integration)
.PHONY: test-no-e2e
test-no-e2e: test-unit test-cmd test-integration

# Run only fast tests (skip slow operations)
.PHONY: test-short
test-short:
	@echo
	@echo "==> Running short tests <=="
	@go test -v -short $(TESTPKG_ALL) $(TESTFLAGS)

# Run tests with coverage
.PHONY: test-cover
test-cover:
	@echo
	@echo "==> Running tests with coverage <=="
	@go test -v -cover -coverprofile=coverage.out $(TESTPKG_ALL) $(TESTFLAGS)
	@go test -v -tags=integration -cover -coverprofile=coverage-integration.out ./integration/... $(TESTFLAGS)
	@echo "Coverage reports: coverage.out, coverage-integration.out"
	@echo "View with: go tool cover -html=coverage.out"

# Run linter
.PHONY: lint
lint:
	@echo
	@echo "==> Running golangci-lint <=="
	@golangci-lint run $(TESTPKG_ALL)
	@golangci-lint run ./integration/...
	@golangci-lint run ./e2e/...

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	@goimports -w cmd/ pkg/ integration/ e2e/ internal/
	@go fmt ./cmd/... ./pkg/... ./integration/... ./e2e/... ./internal/...

# Help
.PHONY: help
help:
	@echo "Test targets:"
	@echo "  make test               - Run ALL tests (unit + cmd + integration + e2e)"
	@echo "  make test-unit          - Run unit tests only (pkg/) - fastest"
	@echo "  make test-cmd           - Run in-process CLI tests (cmd/) - fast"
	@echo "  make test-integration   - Run integration tests - medium"
	@echo "  make test-e2e           - Run E2E tests (binary) - slowest"
	@echo "  make test-no-e2e        - Run all except E2E (unit + cmd + integration)"
	@echo "  make test-short         - Run short tests (skip slow operations)"
	@echo "  make test-cover         - Run tests with coverage report"
	@echo
	@echo "Other targets:"
	@echo "  make build              - Build the binary"
	@echo "  make lint               - Run golangci-lint"
	@echo "  make fmt                - Format code"
	@echo "  make clean              - Remove build artifacts"
	@echo "  make deps               - Download and verify dependencies"
