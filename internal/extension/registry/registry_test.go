package registry

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

type fakeSource struct {
	calls atomic.Int64
	block chan struct{}
}

func (s *fakeSource) Resolve(ctx context.Context, key Key, after time.Time) (Snapshot, error) {
	s.calls.Add(1)
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	return Snapshot{Run: harness.Run{Harness: key.Harness, SessionID: key.RunID, StartedAt: time.Now()}, TraceID: "trace", RootKey: "root", WorkID: "work"}, nil
}
func TestRegistryCopiesAndStatus(t *testing.T) {
	r := New()
	run := harness.Run{Harness: "fake", SessionID: "run", Usage: map[string]harness.Usage{"model": {Input: 1}}, ToolCalls: []harness.ToolCall{{ID: "call", SpawnedSessionIDs: []string{"child"}}}}
	r.Put(Snapshot{Run: run, Segments: []tree.Segment{{Number: 1}}, ToolSpans: map[string]SpanRef{"call": {SpanID: "span"}}})
	run.Usage["model"] = harness.Usage{Input: 9}
	run.ToolCalls[0].SpawnedSessionIDs[0] = "changed"
	got, _ := r.Get(Key{"fake", "run"})
	if got.Run.Usage["model"].Input != 1 || got.Run.ToolCalls[0].SpawnedSessionIDs[0] != "child" {
		t.Fatal("input was not copied")
	}
	got.Run.Usage["model"] = harness.Usage{}
	again, _ := r.Get(Key{"fake", "run"})
	if again.Run.Usage["model"].Input != 1 {
		t.Fatal("output was not copied")
	}
	r.NoteOwner(Key{"fake", "run"}, "native")
	r.Put(Snapshot{Run: run})
	owner, _ := r.Get(Key{"fake", "run"})
	if owner.Run.Owner != "native" || owner.StartedBy != "fake:native" {
		t.Fatal("native owner attribution was not preserved")
	}
	r.NoteNative(Key{"fake", "run"}, time.Unix(1, 0))
	r.IncUnresolved()
	r.IncHashKeyMismatch()
	r.RecordDropped("secret.value")
	body, err := json.Marshal(r.status(&Config{Mode: "agent"}))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		t.Fatal("invalid status")
	}
	for _, key := range []string{"version", "mode", "pid", "user", "uptime_s", "hash_key_id", "hash_key_mismatch", "harnesses", "unresolved", "dropped"} {
		if _, ok := value[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
	if value["harnesses"] == nil || value["dropped"] == nil {
		t.Fatal("null array")
	}
}
func TestRegistryResolveSingleflight(t *testing.T) {
	r := New()
	source := &fakeSource{block: make(chan struct{})}
	r.SetSource(source)
	key := Key{"fake", "run"}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Resolve(context.Background(), key, time.Time{}); err != nil {
				t.Error(err)
			}
		}()
	}
	for source.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(source.block)
	wg.Wait()
	if source.calls.Load() != 1 {
		t.Fatalf("calls=%d", source.calls.Load())
	}
	if _, err := r.Resolve(context.Background(), key, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if source.calls.Load() != 1 {
		t.Fatal("resolve was not throttled")
	}
}
