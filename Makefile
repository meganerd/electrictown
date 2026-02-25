# electrictown — cross-compilation Makefile
# Covers: ET-33 (host build), ET-34 (build-all + dist), ET-35 (verify-cross)

SHELL := /bin/bash

# ─── Variables ───────────────────────────────────────────────────────────────

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOOS       ?= $(shell go env GOOS)
GOARCH     ?= $(shell go env GOARCH)
CGO_ENABLED = 0
MODULE     := github.com/meganerd/electrictown

# Auto-discover all binaries under cmd/
BINARIES   := $(notdir $(wildcard cmd/*))

# All cross-compilation targets (Linux only, zero CGO)
PLATFORMS  := linux/amd64 linux/arm64 linux/riscv64 linux/ppc64 linux/ppc64le

LDFLAGS    := -ldflags "-X main.version=$(VERSION)"

# ─── Default target ──────────────────────────────────────────────────────────

.PHONY: build
build:
	@mkdir -p build
	@for bin in $(BINARIES); do \
		echo "build  $(GOOS)/$(GOARCH)  $$bin"; \
		CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
			go build $(LDFLAGS) -o build/$$bin ./cmd/$$bin; \
	done

# ─── Build all platforms + dist tarballs + checksums (ET-34) ─────────────────

.PHONY: build-all
build-all:
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%%/*}; \
		arch=$${platform##*/}; \
		outdir=dist/$${os}-$${arch}; \
		mkdir -p $$outdir; \
		for bin in $(BINARIES); do \
			echo "build  $${os}/$${arch}  $$bin"; \
			CGO_ENABLED=$(CGO_ENABLED) GOOS=$$os GOARCH=$$arch \
				go build $(LDFLAGS) -o $$outdir/$$bin ./cmd/$$bin; \
		done; \
		tar -czf dist/electrictown-$${os}-$${arch}.tar.gz -C dist $${os}-$${arch}; \
	done
	@cd dist && sha256sum electrictown-*.tar.gz > sha256sums.txt
	@echo ""
	@echo "checksums:"
	@cat dist/sha256sums.txt

# ─── Verify cross-compilation (ET-35) ───────────────────────────────────────

.PHONY: verify-cross
verify-cross:
	@mkdir -p dist
	@ok=true; \
	for platform in $(PLATFORMS); do \
		os=$${platform%%/*}; \
		arch=$${platform##*/}; \
		outdir=dist/$${os}-$${arch}; \
		mkdir -p $$outdir; \
		for bin in $(BINARIES); do \
			echo "build  $${os}/$${arch}  $$bin"; \
			CGO_ENABLED=$(CGO_ENABLED) GOOS=$$os GOARCH=$$arch \
				go build $(LDFLAGS) -o $$outdir/$$bin ./cmd/$$bin || ok=false; \
		done; \
	done; \
	echo ""; \
	echo "=== Binary verification ==="; \
	for platform in $(PLATFORMS); do \
		os=$${platform%%/*}; \
		arch=$${platform##*/}; \
		outdir=dist/$${os}-$${arch}; \
		for bin in $(BINARIES); do \
			printf "%-40s " "$$outdir/$$bin:"; \
			file $$outdir/$$bin | sed 's/^[^:]*: //'; \
		done; \
	done; \
	if [ "$$ok" = "false" ]; then \
		echo ""; \
		echo "ERROR: one or more builds failed"; \
		exit 1; \
	fi; \
	echo ""; \
	echo "All $(words $(PLATFORMS)) platforms verified."

# ─── Test targets ────────────────────────────────────────────────────────────

.PHONY: test
test:
	go test ./... -count=1

.PHONY: test-cover
test-cover:
	go test ./... -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out
	@rm -f coverage.out

# ─── Lint ────────────────────────────────────────────────────────────────────

.PHONY: lint
lint:
	go vet ./...

# ─── Clean ───────────────────────────────────────────────────────────────────

.PHONY: clean
clean:
	rm -rf build dist
