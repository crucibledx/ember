# Ember task runner. Needs GNU make 3.81 or later and a POSIX shell (Git Bash on Windows).
#   make check                   build, vet, race tests and lint: what CI runs on every OS
#   make generate                regenerate cmd/ember/components.go, go.mod and go.sum from builder-config.yaml
#   make dist [VERSION=x.y.z]    five stripped release binaries in dist/ and the size gate
#   make clean                   remove dist/ and build/

GO ?= go
GOLANGCI_LINT ?= golangci-lint

MODULE := github.com/crucibledx/ember
# The ocb release matching the component versions in builder-config.yaml.
OCB_VERSION := v0.166.0

# Spec 0001 §1: release targets and the binary size gate in MiB.
TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64
MAX_BINARY_MIB ?= 80
ifeq ($(strip $(MAX_BINARY_MIB)),)
MAX_BINARY_MIB := 80
endif
# Stamped into `ember version` by `make dist`; empty keeps the default in cmd/ember/main.go.
VERSION ?=
# Recipes read both through the environment, never as make text, so a value cannot inject shell code.
export MAX_BINARY_MIB VERSION

BINARIES := $(foreach target,$(TARGETS),dist/ember_$(subst /,_,$(target))$(if $(filter windows/%,$(target)),.exe))

.DEFAULT_GOAL := check
.PHONY: check build vet test lint generate dist release-version clean FORCE

check: build vet test lint

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

# The race detector needs cgo.
test: export CGO_ENABLED := 1
test:
	$(GO) test -race ./...

lint:
	$(GOLANGCI_LINT) run

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

# Every target binary is rebuilt (the Go build cache keeps that cheap), after the version check.
dist/ember_%: FORCE | release-version
	CGO_ENABLED=0 GOOS=$(word 1,$(subst _, ,$*)) GOARCH=$(basename $(word 2,$(subst _, ,$*))) \
		$(GO) build -trimpath -ldflags "-s -w $${VERSION:+-X main.version=$$VERSION}" -o $@ ./cmd/ember

release-version:
	@if [ -n "$$VERSION" ]; then scripts/check-version.sh "$$VERSION"; fi

clean:
	rm -rf dist build

FORCE:
