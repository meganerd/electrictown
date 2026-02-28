# electrictown — cross-compilation Makefile
#
# Usage:
#   make                       Build for host (dynamic)
#   make static                Build for host (static, CGO_ENABLED=0)
#   make linux-arm64           Build for linux/arm64
#   make linux-arm64-static    Build for linux/arm64 (static)
#   make darwin-amd64          Build for darwin/amd64
#   make windows-arm64-static  Build for windows/arm64 (static)
#   make all                   Build all platforms (dynamic)
#   make all-static            Build all platforms (static)
#   make test                  Run tests
#   make clean                 Remove build artifacts
#   make help                  Show all targets

SHELL := /bin/bash

# ─── Variables ───────────────────────────────────────────────────────────────

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
MODULE     := github.com/meganerd/electrictown
LDFLAGS    := -ldflags "-X main.version=$(VERSION)"

# Auto-discover all binaries under cmd/
BINARIES   := $(notdir $(wildcard cmd/*))

# Supported platforms (os/arch)
PLATFORMS  := linux/amd64 linux/arm64 linux/riscv64 linux/ppc64 linux/ppc64le \
              darwin/amd64 darwin/arm64 \
              windows/amd64 windows/arm64

# Derive make-friendly target names (os-arch)
TARGETS    := $(subst /,-,$(PLATFORMS))

# ─── Internal build function ────────────────────────────────────────────────

# $(1) = GOOS, $(2) = GOARCH, $(3) = CGO_ENABLED (0 or 1)
define do-build
	@mkdir -p build/$(1)-$(2)
	@for bin in $(BINARIES); do \
		ext=""; \
		[ "$(1)" = "windows" ] && ext=".exe"; \
		echo "build  $(1)/$(2)  $$bin$$ext  $(if $(filter 0,$(3)),[static],[dynamic])"; \
		CGO_ENABLED=$(3) GOOS=$(1) GOARCH=$(2) \
			go build $(LDFLAGS) -o build/$(1)-$(2)/$$bin$$ext ./cmd/$$bin; \
	done
endef

# ─── Host build (default) ───────────────────────────────────────────────────

.PHONY: build
build:
	@mkdir -p build
	@for bin in $(BINARIES); do \
		echo "build  $$(go env GOOS)/$$(go env GOARCH)  $$bin  [dynamic]"; \
		go build $(LDFLAGS) -o build/$$bin ./cmd/$$bin; \
	done

.PHONY: static
static:
	@mkdir -p build
	@for bin in $(BINARIES); do \
		echo "build  $$(go env GOOS)/$$(go env GOARCH)  $$bin  [static]"; \
		CGO_ENABLED=0 go build $(LDFLAGS) -o build/$$bin ./cmd/$$bin; \
	done

# ─── Per-platform targets ──────────────────────────────────────────────────

# Generate targets: make linux-arm64, make linux-arm64-static, make darwin-amd64, etc.
# $(1) = os, $(2) = arch
define platform-targets
.PHONY: $(1)-$(2) $(1)-$(2)-static
$(1)-$(2):
	$$(call do-build,$(1),$(2),1)
$(1)-$(2)-static:
	$$(call do-build,$(1),$(2),0)
endef

$(foreach p,$(PLATFORMS),$(eval $(call platform-targets,$(word 1,$(subst /, ,$(p))),$(word 2,$(subst /, ,$(p))))))

# ─── All platforms ─────────────────────────────────────────────────────────

.PHONY: all
all: $(TARGETS)

.PHONY: all-static
all-static: $(addsuffix -static,$(TARGETS))

# ─── Distribution (tarballs + checksums) ─────────────────────────────────────

.PHONY: dist
dist: all-static
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		slug=$${platform//\//-}; \
		tar -czf dist/electrictown-$${slug}.tar.gz -C build $${slug}; \
	done
	@cd dist && sha256sum electrictown-*.tar.gz > sha256sums.txt
	@echo ""
	@echo "checksums:"
	@cat dist/sha256sums.txt

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

# ─── Help ────────────────────────────────────────────────────────────────────

.PHONY: help
help:
	@echo "electrictown build targets:"
	@echo ""
	@echo "  make                       Build for host (dynamic)"
	@echo "  make static                Build for host (static, CGO_ENABLED=0)"
	@echo ""
	@echo "  make <os>-<arch>           Build for specific platform"
	@echo "  make <os>-<arch>-static    Build for specific platform (static)"
	@echo ""
	@echo "  Platforms:"
	@for p in $(PLATFORMS); do \
		slug=$${p//\//-}; \
		echo "    make $$slug          make $${slug}-static"; \
	done
	@echo ""
	@echo "  make all                   Build all platforms"
	@echo "  make all-static            Build all platforms (static)"
	@echo "  make dist                  Build static + tarballs + checksums"
	@echo ""
	@echo "  make test                  Run tests"
	@echo "  make test-cover            Run tests with coverage"
	@echo "  make lint                  Run go vet"
	@echo "  make clean                 Remove build/ and dist/"
