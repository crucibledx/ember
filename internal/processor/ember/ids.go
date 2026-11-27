package ember

import (
	"encoding/hex"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

func decodeTrace(value string) (id pcommon.TraceID, ok bool) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(id) {
		return id, false
	}
	copy(id[:], decoded)
	return id, true
}
func decodeSpan(value string) (id pcommon.SpanID, ok bool) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(id) {
		return id, false
	}
	copy(id[:], decoded)
	return id, true
}
func traceString(id pcommon.TraceID) string { return hex.EncodeToString(id[:]) }
func spanString(id pcommon.SpanID) string   { return hex.EncodeToString(id[:]) }
