package tree

import (
	"slices"
	"time"

	"github.com/crucibledx/ember/internal/harness"
)

type Segment struct {
	Number    int
	Start     time.Time
	End       time.Time
	LLMTurns  int
	ToolCalls int
	Closed    bool
}

func Segments(run harness.Run, idle time.Duration, now time.Time) []Segment {
	times := recordTimes(run)
	if len(times) == 0 {
		return nil
	}
	var result []Segment
	for _, at := range times {
		if len(result) == 0 || at.Sub(result[len(result)-1].End) > idle {
			result = append(result, Segment{Number: len(result) + 1, Start: at, End: at})
		} else if at.After(result[len(result)-1].End) {
			result[len(result)-1].End = at
		}
	}
	for i := range result {
		result[i].Closed = i < len(result)-1 || RunClosed(run, now, idle)
		result[i].LLMTurns, result[i].ToolCalls = segmentCounts(run, result[i])
	}
	return result
}
func Terminal(status harness.RunStatus) bool {
	return status == harness.StatusCompleted || status == harness.StatusCancelled || status == harness.StatusFailed
}
func LastRecord(run harness.Run) time.Time {
	return slices.MaxFunc(recordTimes(run), time.Time.Compare)
}
func RunClosed(run harness.Run, now time.Time, idle time.Duration) bool {
	return Terminal(run.Status) || now.After(LastRecord(run).Add(idle))
}
func recordTimes(run harness.Run) []time.Time {
	times := append([]time.Time{run.StartedAt}, run.TurnTimes...)
	for _, call := range run.ToolCalls {
		times = append(times, call.StartedAt, call.EndedAt)
	}
	times = append(times, run.EndedAt)
	times = slices.DeleteFunc(times, func(at time.Time) bool { return at.IsZero() })
	slices.SortFunc(times, time.Time.Compare)
	return times
}
func segmentCounts(run harness.Run, segment Segment) (int, int) {
	turns, tools := 0, 0
	for _, at := range run.TurnTimes {
		if !at.Before(segment.Start) && !at.After(segment.End) {
			turns++
		}
	}
	for _, call := range run.ToolCalls {
		if !call.StartedAt.Before(segment.Start) && !call.StartedAt.After(segment.End) {
			tools++
		}
	}
	return turns, tools
}
func SegmentAt(segments []Segment, at time.Time, idle time.Duration) (Segment, bool) {
	if len(segments) == 0 {
		return Segment{}, false
	}
	for _, segment := range segments {
		if at.Before(segment.Start) || !at.After(segment.End.Add(idle)) {
			return segment, true
		}
	}
	return Segment{Number: segments[len(segments)-1].Number + 1, Start: at, End: at}, true
}
