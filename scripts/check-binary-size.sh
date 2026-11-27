#!/bin/sh
# Usage: check-binary-size.sh MAX_MIB FILE...
# Prints each file's size and exits 1 when any file is larger than MAX_MIB mebibytes.
# Not a make target: `make dist` runs it on the built binaries, and CI's self-test on files just over and at the limit.
set -eu

if [ "$#" -lt 2 ]; then
	echo "usage: $0 MAX_MIB FILE..." >&2
	exit 2
fi
max_mib=$1
shift
case $max_mib in
'' | *[!0-9]*)
	echo "MAX_MIB must be a whole number, got '$max_mib'" >&2
	exit 2
	;;
esac
max_bytes=$((max_mib * 1024 * 1024))

status=0
for file in "$@"; do
	if [ ! -f "$file" ]; then
		echo "FAIL $file is missing"
		status=1
		continue
	fi
	bytes=$(wc -c <"$file" | tr -d ' ')
	mib=$(awk -v b="$bytes" 'BEGIN { printf "%.1f", b / 1048576 }')
	if [ "$bytes" -gt "$max_bytes" ]; then
		echo "FAIL $(basename "$file") ${mib} MiB (${bytes} bytes) exceeds ${max_mib} MiB"
		status=1
	else
		echo "ok   $(basename "$file") ${mib} MiB (${bytes} bytes)"
	fi
done
exit "$status"
