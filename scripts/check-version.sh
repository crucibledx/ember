#!/bin/sh
# Usage: check-version.sh VERSION
# Exits 0 when VERSION is a SemVer 2.0.0 version without a leading v (e.g. 0.1.0, 1.2.3-rc.1+build.5), else 1.
set -eu
# Bracket ranges in byte order, so [A-Za-z] never matches non-ASCII letters.
export LC_ALL=C

if [ "$#" -ne 1 ]; then
	echo "usage: $0 VERSION" >&2
	exit 2
fi

# grep matches line by line, so a value with a newline could pass on one good line.
case $1 in
*'
'*)
	echo "VERSION must be one line" >&2
	exit 1
	;;
esac

# The grammar from semver.org: no leading zeros in numeric identifiers, no empty identifiers.
num='(0|[1-9][0-9]*)'
pre_id="($num|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
build_id='[0-9A-Za-z-]+'
semver="^$num\\.$num\\.$num(-$pre_id(\\.$pre_id)*)?(\\+$build_id(\\.$build_id)*)?\$"

if ! printf '%s\n' "$1" | grep -Eq "$semver"; then
	echo "VERSION must be SemVer without a leading v, e.g. 0.1.0 or 0.1.0-rc.1, got '$1'" >&2
	exit 1
fi
