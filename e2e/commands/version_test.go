//go:build e2e

package commands_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	res := ember.Run(t, t.TempDir(), "version")
	if res.ExitCode != 0 || !strings.HasPrefix(res.Stdout, "ember ") || res.Stderr != "" {
		t.Errorf("ember version: exit %d\nstdout: %q\nstderr: %q", res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestVersionJSON(t *testing.T) {
	res := ember.Run(t, t.TempDir(), "version", "--json")
	var out map[string]string
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil || res.ExitCode != 0 {
		t.Errorf("ember version --json: exit %d, decode: %v\nstdout: %q\nstderr: %q", res.ExitCode, err, res.Stdout, res.Stderr)
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	res := ember.Run(t, t.TempDir(), "version", "extra")
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, `unknown command "extra"`) {
		t.Errorf("ember version extra: exit %d, want 1 naming the argument\nstderr: %q", res.ExitCode, res.Stderr)
	}
}
