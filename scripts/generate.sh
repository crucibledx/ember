#!/bin/sh
# Regenerates cmd/ember/components.go and the collector requirements in go.mod from builder-config.yaml.
# Run from anywhere with Go on PATH; CI fails when the committed files differ from the output.
set -eu

# The ocb release matching the component versions in builder-config.yaml.
OCB_VERSION=v0.166.0
MODULE=github.com/crucibledx/ember

cd "$(dirname "$0")/.."
# ocb creates only the last element of output_path.
rm -rf build/ocb
mkdir -p build
go run "go.opentelemetry.io/collector/cmd/builder@${OCB_VERSION}" --config builder-config.yaml --skip-compilation
cp build/ocb/components.go cmd/ember/components.go
gofmt -w cmd/ember/components.go

# ocb resolved the collector's module graph in build/ocb; pin the same versions here so this module builds exactly
# what the manifest names, then let tidy drop what Ember does not import.
# The assignment makes set -e catch a failing go list, which a pipeline would hide.
requirements=$(cd build/ocb && go list -m -f "{{if and .Version (not .Main) (ne .Path \"${MODULE}\")}}-require={{.Path}}@{{.Version}}{{end}}" all)
printf '%s\n' "$requirements" | xargs go mod edit
go mod tidy
