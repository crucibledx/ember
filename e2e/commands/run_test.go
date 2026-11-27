//go:build e2e

package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `receivers:
  otlp:
    protocols:
      http:
        endpoint: 127.0.0.1:4318
processors:
  batch:
exporters:
  debug:
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
`

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "collector.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunValidate(t *testing.T) {
	home := t.TempDir()
	res := ember.Run(t, home, "run", "validate", "--config", writeConfig(t, home, validConfig))
	if res.ExitCode != 0 {
		t.Errorf("ember run validate: exit %d\nstderr: %s", res.ExitCode, res.Stderr)
	}
}

func TestRunValidateRejectsUnknownComponent(t *testing.T) {
	home := t.TempDir()
	config := strings.ReplaceAll(validConfig, "debug", "nonexistent")
	res := ember.Run(t, home, "run", "validate", "--config", writeConfig(t, home, config))
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "nonexistent") {
		t.Errorf("ember run validate with an unknown exporter: exit %d, want 1 naming it\nstderr: %s", res.ExitCode, res.Stderr)
	}
}
