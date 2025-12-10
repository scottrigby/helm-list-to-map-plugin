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
