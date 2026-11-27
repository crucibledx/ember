# Ember task runner. Needs GNU make 3.81 or later and a POSIX shell (Git Bash on Windows).
#   make check                   build, vet, race tests and lint: what CI runs on every OS
#   make fmt                     gofmt and goimports through golangci-lint
#   make generate                regenerate cmd/ember/components.go, go.mod and go.sum from builder-config.yaml
#   make dist [VERSION=x.y.z]    five stripped release binaries in dist/ and the size gate
#   make clean                   remove dist/ and build/

GO ?= go
GOLANGCI_LINT ?= golangci-lint
# The one pin for golangci-lint: CI installs this version (make golangci-lint-version); use the same one locally.
GOLANGCI_LINT_VERSION := v2.15.0

MODULE := github.com/crucibledx/ember
# The ocb release matching the component versions in builder-config.yaml.
OCB_VERSION := v0.166.0

# Spec 0001 §1: release targets and the binary size gate in MiB.
TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64
# VERSION is stamped into `ember version` by `make dist`; empty keeps the default in cmd/ember/main.go.
# Both come from the command line or the environment and are untrusted. $(value) copies them unexpanded into simple
# variables, so a `$(shell ...)` or `$x` in them is never run or substituted by make. Recipes read them only through
# the environment, quoted, and release-inputs validates both before any build.
override VERSION := $(value VERSION)
override MAX_BINARY_MIB := $(value MAX_BINARY_MIB)
ifeq ($(MAX_BINARY_MIB),)
override MAX_BINARY_MIB := 80
endif
export MAX_BINARY_MIB VERSION

BINARIES := $(foreach target,$(TARGETS),dist/ember_$(subst /,_,$(target))$(if $(filter windows/%,$(target)),.exe))

.DEFAULT_GOAL := check
.PHONY: check build vet test lint fmt golangci-lint-version generate dist release-inputs clean FORCE

check: build vet test lint

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

# The race detector needs cgo.
test:
	CGO_ENABLED=1 $(GO) test -race ./...

lint:
	$(GOLANGCI_LINT) run

fmt:
	$(GOLANGCI_LINT) fmt

golangci-lint-version:
	@echo $(GOLANGCI_LINT_VERSION)

# ocb creates only the last element of output_path, hence the mkdir. It resolves the collector's module graph in
# build/ocb; the go list line pins the same versions here so this module builds exactly what the manifest names, then
# tidy drops what Ember does not import.
generate:
	rm -rf build/ocb
	mkdir -p build
	$(GO) run go.opentelemetry.io/collector/cmd/builder@$(OCB_VERSION) --config builder-config.yaml --skip-compilation
	cp build/ocb/components.go cmd/ember/components.go
	gofmt -w cmd/ember/components.go
	cd build/ocb && $(GO) list -m -f '{{if and .Version (not .Main) (ne .Path "$(MODULE)")}}-require={{.Path}}@{{.Version}}{{end}}' all >../requirements.txt
	xargs $(GO) mod edit <build/requirements.txt
	$(GO) mod tidy

dist: $(BINARIES)
	scripts/check-binary-size.sh "$$MAX_BINARY_MIB" $(BINARIES)

# Every target binary is rebuilt (the Go build cache keeps that cheap), after the input checks.
dist/ember_%: FORCE | release-inputs
	CGO_ENABLED=0 GOOS=$(word 1,$(subst _, ,$*)) GOARCH=$(basename $(word 2,$(subst _, ,$*))) \
		$(GO) build -trimpath -ldflags "-s -w $${VERSION:+-X main.version=$$VERSION}" -o $@ ./cmd/ember

release-inputs:
	@case "$$MAX_BINARY_MIB" in *[!0-9]*) echo "MAX_BINARY_MIB must be a whole number, got '$$MAX_BINARY_MIB'" >&2; exit 2;; esac
	@if [ -n "$$VERSION" ]; then scripts/check-version.sh "$$VERSION"; fi

clean:
	rm -rf dist build

FORCE:
