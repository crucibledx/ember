// Command binarysize prints the size of each release binary and fails when one is larger than the limit (spec 0001
// §1). `make dist` runs it on the five targets.
//
// Usage: go run ./internal/tools/binarysize MAX_MIB FILE [FILE ...]
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const mib = 1 << 20

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run returns 0 when every file is within the limit, 1 when one is over it or missing, and 2 for bad arguments.
func run(args []string, stdout, stderr io.Writer) int {
	report, withinLimit, err := check(args)
	if err != nil {
		// Nothing is left to report a failed write of the usage error to; the exit code still says it.
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if _, err := io.WriteString(stdout, report); err != nil || !withinLimit {
		return 1
	}
	return 0
}

// check returns one report line per file and whether all of them are within the limit.
func check(args []string) (string, bool, error) {
	if len(args) < 2 {
		return "", false, errors.New("usage: binarysize MAX_MIB FILE [FILE ...]")
	}
	maxMiB, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		return "", false, fmt.Errorf("MAX_MIB must be a whole number, got %q", args[0])
	}
	limit := int64(maxMiB) * mib
	var report strings.Builder
	withinLimit := true
	for _, file := range args[1:] {
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			fmt.Fprintf(&report, "FAIL %s is missing\n", file)
			withinLimit = false
			continue
		}
		size, name := info.Size(), filepath.Base(file)
		if size > limit {
			fmt.Fprintf(&report, "FAIL %s %.1f MiB (%d bytes) exceeds %d MiB\n", name, float64(size)/mib, size, maxMiB)
			withinLimit = false
			continue
		}
		fmt.Fprintf(&report, "ok   %s %.1f MiB (%d bytes)\n", name, float64(size)/mib, size)
	}
	return report.String(), withinLimit, nil
}
