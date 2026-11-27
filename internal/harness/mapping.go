package harness

import "regexp"

// Mapping describes how a harness represents sessions and tools in native OTLP.
type Mapping struct {
	// Resource maps resource attributes to accepted values.
	Resource map[string][]string

	SessionKey   string
	SubagentKey  string
	ToolCallKeys []string
	ToolNameKeys []string
	// ToolSpanName matches an exact span name or a prefix ending in a space.
	ToolSpanName  string
	RedactedTools []string
	// MCP extracts server and tool from the first matching separator.
	MCP *regexp.Regexp

	NativeAllow []string
	ContentKeys []string
	OwnerKeys   []string
	Metrics     NativeMetrics
}

// NativeMetrics identifies native metrics used by Ember's dashboards.
type NativeMetrics struct {
	Cost     string
	CostUnit string
}
