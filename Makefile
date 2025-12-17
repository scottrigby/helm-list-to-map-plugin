# Module/binary settings
BINARY := list-to-map
PKG := ./cmd

# Build output path
BIN_DIR := bin
BIN_PATH := $(BIN_DIR)/$(BINARY)

# Version via git tag or commit
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0")

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

# Verify module and dependencies
.PHONY: deps
deps:
	@go mod tidy
	@go mod verify

# Run all tests
.PHONY: test
test:
	@echo "Running tests..."
	@go test -v ./cmd/... ./pkg/...

# Run only unit tests (pkg/)
.PHONY: test-unit
test-unit:
	@echo "Running unit tests..."
	@go test -v ./pkg/...

# Run only integration/E2E tests (cmd/)
.PHONY: test-integration
test-integration:
	@echo "Running integration/E2E tests..."
	@go test -v ./cmd/...

# Run only E2E tests (tests that exec the binary)
.PHONY: test-e2e
test-e2e:
	@echo "Running E2E tests (binary execution)..."
	@go test -v ./cmd/... -run 'TestCLI|TestSubchart'

# Run tests with coverage
.PHONY: test-cover
test-cover:
	@echo "Running tests with coverage..."
	@go test -v -coverprofile=coverage.out ./cmd/... ./pkg/...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run tests in short mode (skip slow tests)
.PHONY: test-short
test-short:
	@echo "Running tests (short mode)..."
	@go test -v -short ./cmd/... ./pkg/...

# Run linter
.PHONY: lint
lint:
	@echo "Running golangci-lint..."
	@golangci-lint run ./cmd/... ./pkg/...

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	@goimports -w cmd/ pkg/
	@go fmt ./cmd/... ./pkg/...

# Help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  make build              - Build the binary"
	@echo "  make clean              - Remove build artifacts"
	@echo "  make deps               - Download and verify dependencies"
	@echo "  make test               - Run all tests (unit + integration)"
	@echo "  make test-unit          - Run unit tests only (pkg/...)"
	@echo "  make test-integration   - Run integration/E2E tests (cmd/...)"
	@echo "  make test-e2e           - Run only E2E tests (binary execution)"
	@echo "  make test-cover         - Run tests with coverage report"
	@echo "  make test-short         - Run only fast tests (skip slow E2E)"
	@echo "  make lint               - Run golangci-lint"
	@echo "  make fmt                - Format code with goimports"
	@echo "  make help               - Show this help message"
