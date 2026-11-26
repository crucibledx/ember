#!/bin/sh
# Usage: check-binary-size.sh MAX_MIB FILE...
# Prints each file's size and exits 1 when any file is larger than MAX_MIB mebibytes.
set -eu

if [ "$#" -lt 2 ]; then
	echo "usage: $0 MAX_MIB FILE..." >&2
	exit 2
fi
max_mib=$1
shift
max_bytes=$((max_mib * 1024 * 1024))

status=0
for file in "$@"; do
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
