package registry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/extension/extensiontest"

	"github.com/crucibledx/ember/internal/harness"
)

func TestNativeToolLinksOnlyCommittedSpans(t *testing.T) {
	key := Key{Harness: "fake", RunID: "run"}
	r := New()
	r.Put(Snapshot{Run: harness.Run{Harness: "fake", SessionID: "run"}, ToolSpans: map[string]SpanRef{"call": {TraceID: "trace", SpanID: "file"}}})
	if _, ok := r.ReserveFile(key, "call", time.Now(), time.Minute); !ok {
		t.Fatal("reserve")
	}
	id, ref, linked := r.NativeTool(key, "call", "native")
	if id == "" || linked || ref.TraceID != "" {
		t.Fatal("reserved span linked")
	}
	r.RollbackFile(key, "call")
	if _, ok := r.ReserveFile(key, "committed", time.Now(), time.Minute); !ok {
		t.Fatal("reserve committed")
	}
	r.CommitFile(key, "committed")
	r.Put(Snapshot{Run: harness.Run{Harness: "fake", SessionID: "run"}, ToolSpans: map[string]SpanRef{"committed": {TraceID: "trace", SpanID: "committed-file"}}})
	if _, ref, linked := r.NativeTool(key, "committed", "native"); !linked || ref.TraceID == "" {
		t.Fatal("committed span not linked")
	}
	r.RestoreFile(key, "restart", SpanRef{})
	if _, ref, linked = r.NativeTool(key, "restart", "native"); linked || ref.TraceID != "" {
		t.Fatal("empty restart link")
	}
	r.Put(Snapshot{Run: harness.Run{Harness: "fake", SessionID: "run"}, ToolSpans: map[string]SpanRef{"restart": {TraceID: "trace", SpanID: "file"}}})
	if _, ref, linked = r.NativeTool(key, "restart", "native"); !linked || ref.TraceID != "trace" {
		t.Fatal("restored emitted span not linked after snapshot")
	}
}
func TestNativeLastSeenMonotonic(t *testing.T) {
	r := New()
	key := Key{"fake", "run"}
	r.NoteNative(key, time.Unix(10, 0))
	r.NoteNative(key, time.Unix(5, 0))
	at, _ := r.NativeLast(key)
	if !at.Equal(time.Unix(10, 0)) {
		t.Fatal("native last seen moved backward")
	}
	r.RestoreNativeFirst(key, time.Unix(1, 0))
	at, _ = r.NativeLast(key)
	if !at.Equal(time.Unix(10, 0)) {
		t.Fatal("first-seen restore changed last-seen")
	}
}
func TestStatusImmediateShutdown(t *testing.T) {
	factory := NewFactory()
	for range 25 {
		cfg := factory.CreateDefaultConfig().(*Config)
		cfg.Endpoint = "127.0.0.1:0"
		ext, err := factory.Create(context.Background(), extensiontest.NewNopSettings(component.MustNewType("ember")), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err = ext.Start(context.Background(), componenttest.NewNopHost()); err != nil {
			t.Fatal(err)
		}
		if err = ext.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
