//go:build e2e

package contracts_test

import (
	"encoding/json"
	"maps"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Spec 0001 §2 and AC-1: the text form of `ember version`.
var versionLine = regexp.MustCompile(`^ember (\S+) \((\S+), (\S+)\) otelcol (\d+\.\d+\.\d+) (\S+)/(\S+)\n$`)

func TestVersionTextFormat(t *testing.T) {
	res := ember.Run(t, t.TempDir(), "version")
	m := versionLine.FindStringSubmatch(res.Stdout)
	if res.ExitCode != 0 || m == nil {
		t.Fatalf("ember version: exit %d, output %q does not match %s", res.ExitCode, res.Stdout, versionLine)
	}
	if m[5] != runtime.GOOS || m[6] != runtime.GOARCH {
		t.Errorf("platform = %s/%s, want %s/%s", m[5], m[6], runtime.GOOS, runtime.GOARCH)
	}
}

// AC-1: `ember version --json` has exactly these string fields; scripts parse them.
func TestVersionJSONFields(t *testing.T) {
	res := ember.Run(t, t.TempDir(), "version", "--json")
	var out map[string]string
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		t.Fatalf("decode %q: %v", res.Stdout, err)
	}
	want := []string{"arch", "commit", "date", "os", "otelcol", "version"}
	if got := slices.Sorted(maps.Keys(out)); !slices.Equal(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
	if out["os"] != runtime.GOOS || out["arch"] != runtime.GOARCH {
		t.Errorf("platform = %s/%s, want %s/%s", out["os"], out["arch"], runtime.GOOS, runtime.GOARCH)
	}
}

// All commands are non-interactive and fail with exit 1 and a message on stderr.
func TestUnknownCommandExitsOne(t *testing.T) {
	res := ember.Run(t, t.TempDir(), "no-such-command")
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, `unknown command "no-such-command"`) {
		t.Errorf("exit %d, want 1 with the command named\nstderr: %q", res.ExitCode, res.Stderr)
	}
}
