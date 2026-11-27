package registry

import (
	"time"

	"github.com/crucibledx/ember/internal/tree"
)

func (r *Registry) NoteNative(key Key, at time.Time) {
	r.mu.Lock()
	if at.After(r.nativeLast[key]) {
		r.nativeLast[key] = at
	}
	if old := r.native[key]; old.IsZero() || at.Before(old) {
		r.native[key] = at
	}
	r.mu.Unlock()
}
func (r *Registry) RestoreNativeFirst(key Key, at time.Time) {
	r.mu.Lock()
	if old := r.native[key]; old.IsZero() || at.Before(old) {
		r.native[key] = at
	}
	r.mu.Unlock()
}
func (r *Registry) NativeFirst(key Key) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	at, ok := r.native[key]
	return at, ok
}
func (r *Registry) NativeLast(key Key) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	at, ok := r.nativeLast[key]
	return at, ok
}
func (r *Registry) RestoreNativeLast(key Key, at time.Time) {
	r.mu.Lock()
	if at.After(r.nativeLast[key]) {
		r.nativeLast[key] = at
	}
	r.mu.Unlock()
}
func spanFor(key Key, callID string) SpanRef {
	return SpanRef{SpanID: tree.ToolSpanID(key.Harness, callID)}
}
func (r *Registry) ReserveFile(key Key, callID string, now time.Time, grace time.Duration) (SpanRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nativeCalls[key][callID] != "" {
		return SpanRef{}, false
	}
	if first := r.native[key]; !first.IsZero() && !now.Before(first.Add(grace)) {
		return SpanRef{}, false
	}
	if ref := r.emitted[key][callID]; ref.SpanID != "" {
		return ref, true
	}
	if ref := r.reserved[key][callID]; ref.SpanID != "" {
		return ref, false
	}
	if r.reserved[key] == nil {
		r.reserved[key] = map[string]SpanRef{}
	}
	ref := spanFor(key, callID)
	r.reserved[key][callID] = ref
	return ref, true
}
func (r *Registry) CommitFile(key Key, callID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ref := r.reserved[key][callID]
	if ref.SpanID == "" {
		ref = spanFor(key, callID)
	}
	if r.emitted[key] == nil {
		r.emitted[key] = map[string]SpanRef{}
	}
	if ref.TraceID == "" {
		ref.TraceID = r.snaps[key].ToolSpans[callID].TraceID
	}
	r.emitted[key][callID] = ref
	delete(r.reserved[key], callID)
}
func (r *Registry) RollbackFile(key Key, callID string) {
	r.mu.Lock()
	delete(r.reserved[key], callID)
	r.mu.Unlock()
}
func (r *Registry) RestoreFile(key Key, callID string, ref SpanRef) {
	r.mu.Lock()
	if r.emitted[key] == nil {
		r.emitted[key] = map[string]SpanRef{}
	}
	if ref.SpanID == "" {
		ref = spanFor(key, callID)
	}
	r.emitted[key][callID] = ref
	r.mu.Unlock()
}
func (r *Registry) RestoreNativeTool(key Key, callID, nativeID string) {
	r.mu.Lock()
	if r.nativeCalls[key] == nil {
		r.nativeCalls[key] = map[string]string{}
	}
	r.nativeCalls[key][callID] = nativeID
	r.mu.Unlock()
}
func (r *Registry) NativeCalls(key Key) map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]string{}
	for id, nativeID := range r.nativeCalls[key] {
		out[id] = nativeID
	}
	return out
}
func (r *Registry) NativeTool(key Key, callID, nativeID string) (string, SpanRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nativeCalls[key] == nil {
		r.nativeCalls[key] = map[string]string{}
	}
	r.nativeCalls[key][callID] = nativeID
	ref := r.emitted[key][callID]
	if ref.TraceID == "" {
		ref.TraceID = r.snaps[key].ToolSpans[callID].TraceID
	}
	if ref.SpanID != "" && ref.TraceID != "" {
		return nativeID, ref, true
	}
	return tree.ToolSpanID(key.Harness, callID), SpanRef{}, false
}
