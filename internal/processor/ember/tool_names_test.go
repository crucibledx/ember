package ember

import (
	"regexp"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/crucibledx/ember/internal/harness"
)

func TestToolNames(t *testing.T) {
	for _, test := range []struct {
		name, native, restored, server, tool string
		restore                              bool
	}{
		{name: "restore and split", native: "hidden", restored: "mcp__server__tool", server: "server", tool: "tool", restore: true},
		{name: "switch off", native: "hidden", restored: "hidden", restore: false},
		{name: "unredacted still splits", native: "mcp__server__tool", restored: "mcp__server__tool", server: "server", tool: "tool", restore: false},
		{name: "only exact redacted name", native: "hidden extra", restored: "hidden extra", restore: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := newTestProcessor(harness.Mapping{})
			p.config.Privacy.RestoreToolNames = test.restore
			snapshot := testSnapshot(p, "run")
			snapshot.Run.ToolCalls = []harness.ToolCall{{ID: "call", Name: "mcp__server__tool"}}
			p.registry.Put(snapshot)
			traces, resource := testTraces()
			span := addSpan(resource, testTrace(1), testSpan(1), pcommon.SpanID{}, "tool exact", "run")
			span.Attributes().PutStr("call.id", "call")
			span.Attributes().PutStr("tool.name", test.native)
			processTestTraces(t, p, traces)
			if got := stringAttr(span.Attributes(), "tool.name"); got != test.restored {
				t.Errorf("tool name = %q, want %q", got, test.restored)
			}
			if got := stringAttr(span.Attributes(), "ember.mcp.server"); got != test.server {
				t.Errorf("server = %q, want %q", got, test.server)
			}
			if got := stringAttr(span.Attributes(), "ember.mcp.tool"); got != test.tool {
				t.Errorf("tool = %q, want %q", got, test.tool)
			}
		})
	}

	t.Run("triple separator keeps full semantic name", func(t *testing.T) {
		mapping := syntheticMapping()
		mapping.ToolSpanName = "exact"
		mapping.ToolNameKeys = []string{"gen_ai.tool.name"}
		mapping.MCP = regexp.MustCompile(`^(.+?)___(.+)$`)
		p := newTestProcessor(mapping)
		testSnapshot(p, "run")
		traces, resource := testTraces()
		span := addSpan(resource, testTrace(2), testSpan(1), pcommon.SpanID{}, "exact", "run")
		span.Attributes().PutStr("call.id", "call")
		span.Attributes().PutStr("gen_ai.tool.name", "server___tool")
		processTestTraces(t, p, traces)
		if stringAttr(span.Attributes(), "gen_ai.tool.name") != "server___tool" ||
			stringAttr(span.Attributes(), "ember.mcp.server") != "server" || stringAttr(span.Attributes(), "ember.mcp.tool") != "tool" {
			t.Fatalf("MCP attributes = %v", span.Attributes().AsRaw())
		}
	})
}
