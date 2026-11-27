package cli

import (
	"bytes"
	"testing"

	"go.opentelemetry.io/collector/otelcol"

	"github.com/crucibledx/ember/internal/buildinfo"
)

func TestVersion(t *testing.T) {
	info := buildinfo.Info{
		Version: "0.1.0",
		Commit:  "734252e",
		Date:    "2026-11-26T10:00:00Z",
		OtelCol: "0.166.0",
		OS:      "darwin",
		Arch:    "arm64",
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "text",
			args: []string{"version"},
			want: "ember 0.1.0 (734252e, 2026-11-26T10:00:00Z) otelcol 0.166.0 darwin/arm64\n",
		},
		{
			name: "json",
			args: []string{"version", "--json"},
			want: `{"version":"0.1.0","commit":"734252e","date":"2026-11-26T10:00:00Z","otelcol":"0.166.0","os":"darwin","arch":"arm64"}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCommand(info, otelcol.CollectorSettings{})
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(tt.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("output\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}
