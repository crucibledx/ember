package tree

import (
	"sort"
	"strings"
	"time"
)

type AttributionRecord struct {
	At        time.Time
	Human     string
	Source    string
	SessionID string
	Role      string
}

func (r AttributionRecord) id() string { return r.Source + ":" + r.Human }
func applyAttribution(node *Node, records []AttributionRecord, fallback string, now time.Time) {
	node.StartedBy = earliestStarted(node.Run.SessionID, records)
	if node.StartedBy == "" && node.parent != nil {
		node.StartedBy = node.parent.StartedBy
	}
	if node.StartedBy == "" && node.Run.Owner != "" {
		node.StartedBy = node.Run.Harness + ":" + node.Run.Owner
	}
	if node.StartedBy == "" {
		node.StartedBy = fallback
	}
	active := map[string]bool{}
	if node.StartedBy != "" {
		active[node.StartedBy] = true
	}
	collectActive(node, records, active, now)
	node.Active = make([]string, 0, len(active))
	for id := range active {
		node.Active = append(node.Active, id)
	}
	sort.Strings(node.Active)
}
func earliestStarted(runID string, records []AttributionRecord) string {
	var at time.Time
	id := ""
	for _, record := range records {
		if record.SessionID == runID && record.Role == "started" && record.Human != "" &&
			(id == "" || record.At.Before(at)) {
			at, id = record.At, record.id()
		}
	}
	return id
}
func collectActive(node *Node, records []AttributionRecord, active map[string]bool, now time.Time) {
	end := node.Run.EndedAt
	if end.IsZero() {
		end = now
	}
	for _, record := range records {
		if record.SessionID == node.Run.SessionID && record.Human != "" &&
			!record.At.Before(node.Run.StartedAt) && !record.At.After(end) {
			active[record.id()] = true
		}
	}
	for _, child := range node.Children {
		collectActive(child, records, active, now)
	}
}
func ActiveHumanIDs(ids []string) string { return strings.Join(ids, ",") }
