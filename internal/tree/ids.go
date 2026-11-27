package tree

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

func digest(value string, length int) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:length]
}
func TraceID(rootKey string) string { return digest("ember/trace/"+rootKey, 32) }
func SessionSpanID(harnessName, runID string) string {
	return digest("ember/span/"+harnessName+"/session/"+runID, 16)
}
func ToolSpanID(harnessName, callID string) string {
	return digest("ember/span/"+harnessName+"/tool/"+callID, 16)
}
func SegmentSpanID(harnessName, runID string, segment int) string {
	return digest("ember/span/"+harnessName+"/session/"+runID+"/segment/"+strconv.Itoa(segment), 16)
}
func WorkstreamRootKey(id string) string              { return "workstream/" + id }
func SessionRootKey(harnessName, runID string) string { return "session/" + harnessName + "/" + runID }
