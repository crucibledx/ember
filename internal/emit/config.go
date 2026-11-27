package emit

import (
	"cmp"
	"encoding/hex"
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

type Resource struct {
	Version   string
	HostName  string
	OSType    string
	HostArch  string
	HashKeyID string
}
type Config struct {
	Resource Resource
	MCP      map[string]*regexp.Regexp
}

func resourceFor(out pcommon.Resource, node *tree.Node, config Config) {
	attrs := out.Attributes()
	putStrings(attrs, "service.name", "ember", "service.version", config.Resource.Version, "service.instance.id", node.Run.SessionID)
	host := cmp.Or(node.Run.HostID, config.Resource.HostName)
	putStrings(attrs, "host.name", host, "os.type", config.Resource.OSType,
		"host.arch", config.Resource.HostArch, "ember.hash_key.id", config.Resource.HashKeyID)
}
func putString(attrs pcommon.Map, key, value string) {
	if value != "" {
		attrs.PutStr(key, value)
	}
}
func putStrings(attrs pcommon.Map, values ...string) {
	for i := 0; i < len(values); i += 2 {
		putString(attrs, values[i], values[i+1])
	}
}
func traceID(value string) (id pcommon.TraceID) {
	_, _ = hex.Decode(id[:], []byte(value))
	return id
}
func spanID(value string) (id pcommon.SpanID) {
	_, _ = hex.Decode(id[:], []byte(value))
	return id
}
func timestamp(at interface{ UnixNano() int64 }) pcommon.Timestamp {
	return pcommon.Timestamp(at.UnixNano())
}
func SegmentSpanID(node *tree.Node, segment int) string {
	if segment <= 1 {
		return tree.SessionSpanID(node.Run.Harness, node.Run.SessionID)
	}
	return tree.SegmentSpanID(node.Run.Harness, node.Run.SessionID, segment)
}
func mcpTool(config Config, node *tree.Node, name string) (string, string) {
	pattern := config.MCP[node.Run.Harness]
	if pattern == nil {
		return "", ""
	}
	match := pattern.FindStringSubmatch(name)
	if len(match) < 3 {
		return "", ""
	}
	return match[1], match[2]
}
func spawnedIDs(ids []string) string { return strings.Join(ids, ",") }
func runOpen(node *tree.Node) bool {
	return len(node.Segments) != 0 && !node.Segments[len(node.Segments)-1].Closed
}
func toolEligible(node *tree.Node, call harness.ToolCall) bool {
	return !runOpen(node) || !call.EndedAt.IsZero()
}
