#!/bin/sh
# Regression cases for check-version.sh. Run from anywhere; exits 1 on the first wrong answer.
set -eu

cd "$(dirname "$0")"
failed=0

for v in 0.1.0 1.2.3 10.20.30 0.1.0-dev 1.0.0-rc.1 1.0.0-0 1.0.0-alpha.0valid 1.0.0-x-y-z.-- 1.0.0+build.5 \
	1.0.0-rc.1+20261126.sha.734252e 1.0.0+001; do
	if ! ./check-version.sh "$v" 2>/dev/null; then
		echo "rejected valid version '$v'"
		failed=1
	fi
done

newline='1.0.0
x'
for v in '' v0.1.0 1.2 1.2.3.4 01.2.3 1.02.3 1.2.03 1.2.3-01 1.2.3-.. 1.2.3- 1.2.3-a..b 1.2.3+ 1.2.3+. \
	1.2.3+a..b '1.2.3 -X main.commit=x' 1.2.3-ä "$newline"; do
	if ./check-version.sh "$v" 2>/dev/null; then
		echo "accepted invalid version '$v'"
		failed=1
	fi
done

exit "$failed"
