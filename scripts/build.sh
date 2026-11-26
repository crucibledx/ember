#!/bin/sh
# Usage: build.sh [VERSION]
# Builds stripped ember binaries for the release targets into dist/ and checks their size (spec 0001 §1).
# VERSION (SemVer, no leading v; checked by check-version.sh) is stamped into `ember version`; commit and date come from the git checkout.
set -eu

TARGETS="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64"
MAX_MIB=${MAX_BINARY_MIB:-80}

cd "$(dirname "$0")/.."
ldflags="-s -w"
if [ -n "${1:-}" ]; then
	scripts/check-version.sh "$1"
	ldflags="$ldflags -X main.version=$1"
fi

mkdir -p dist
binaries=""
for target in $TARGETS; do
	os=${target%/*}
	arch=${target#*/}
	ext=""
	if [ "$os" = windows ]; then
		ext=.exe
	fi
	out="dist/ember_${os}_${arch}${ext}"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$out" ./cmd/ember
	binaries="$binaries $out"
done

# shellcheck disable=SC2086 # word splitting of the binary list is intended
scripts/check-binary-size.sh "$MAX_MIB" $binaries
