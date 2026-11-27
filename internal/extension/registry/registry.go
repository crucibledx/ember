// Package registry implements Ember's shared session registry extension.
package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

type Key struct{ Harness, RunID string }
type SpanRef struct{ TraceID, SpanID string }
type Snapshot struct {
	Run                                     harness.Run
	TraceID, RootKey, RootSessionID, WorkID string
	Segments                                []tree.Segment
	ToolSpans                               map[string]SpanRef
	Idle                                    time.Duration
	LastRecord                              time.Time
	StartedBy                               string
	WorkstreamID                            string
	Active                                  []string
}
type Source interface {
	Resolve(context.Context, Key, time.Time) (Snapshot, error)
}
type parent struct {
	key  Key
	call string
}
type resolveCall struct {
	done chan struct{}
	err  error
}

type Registry struct {
	mu                       sync.RWMutex
	snaps                    map[Key]Snapshot
	parents                  map[Key]parent
	owners                   map[string]Key
	native                   map[Key]time.Time
	nativeLast               map[Key]time.Time
	nativeCalls              map[Key]map[string]string
	emitted                  map[Key]map[string]SpanRef
	reserved                 map[Key]map[string]SpanRef
	source                   Source
	lastResolve              map[Key]time.Time
	inflight                 map[Key]*resolveCall
	unresolved, hashMismatch uint64
	dropped                  map[string]uint64
	started                  time.Time
}

func New() *Registry {
	return &Registry{snaps: map[Key]Snapshot{}, parents: map[Key]parent{}, owners: map[string]Key{}, native: map[Key]time.Time{}, nativeLast: map[Key]time.Time{}, nativeCalls: map[Key]map[string]string{}, emitted: map[Key]map[string]SpanRef{}, reserved: map[Key]map[string]SpanRef{}, lastResolve: map[Key]time.Time{}, inflight: map[Key]*resolveCall{}, dropped: map[string]uint64{}, started: time.Now()}
}
func FromHost(host component.Host, id component.ID) (*Registry, error) {
	extension, ok := host.GetExtensions()[id]
	if !ok {
		return nil, fmt.Errorf("registry extension %s is not configured", id)
	}
	r, ok := extension.(*Extension)
	if !ok {
		return nil, fmt.Errorf("extension %s is not an Ember registry", id)
	}
	return r.registry, nil
}
func (r *Registry) SetSource(source Source) { r.mu.Lock(); r.source = source; r.mu.Unlock() }
func copyRun(in harness.Run) harness.Run {
	out := in
	if in.Parent != nil {
		p := *in.Parent
		out.Parent = &p
	}
	out.Usage = make(map[string]harness.Usage, len(in.Usage))
	for k, v := range in.Usage {
		out.Usage[k] = v
	}
	out.TurnTimes = append([]time.Time(nil), in.TurnTimes...)
	out.ToolCalls = append([]harness.ToolCall(nil), in.ToolCalls...)
	for i := range out.ToolCalls {
		out.ToolCalls[i].SpawnedSessionIDs = append([]string(nil), in.ToolCalls[i].SpawnedSessionIDs...)
	}
	return out
}
func copySnapshot(in Snapshot) Snapshot {
	out := in
	out.Run = copyRun(in.Run)
	out.Segments = append([]tree.Segment(nil), in.Segments...)
	out.Active = append([]string(nil), in.Active...)
	out.ToolSpans = make(map[string]SpanRef, len(in.ToolSpans))
	for k, v := range in.ToolSpans {
		out.ToolSpans[k] = v
	}
	return out
}
func (r *Registry) Put(snapshot Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot = copySnapshot(snapshot)
	key := Key{snapshot.Run.Harness, snapshot.Run.SessionID}
	if previous, ok := r.snaps[key]; ok {
		if snapshot.Run.Owner == "" {
			snapshot.Run.Owner = previous.Run.Owner
		}
		if snapshot.StartedBy == "" {
			snapshot.StartedBy = previous.StartedBy
		}
	}
	if snapshot.StartedBy == "" && snapshot.Run.Owner != "" {
		snapshot.StartedBy = snapshot.Run.Harness + ":" + snapshot.Run.Owner
	}
	r.snaps[key] = snapshot
	for _, call := range snapshot.Run.ToolCalls {
		r.owners[ownerKey(key.Harness, call.ID)] = key
	}
}
func (r *Registry) Get(key Key) (Snapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.snaps[key]
	return copySnapshot(s), ok
}
func (r *Registry) Segment(key Key, at time.Time) (tree.Segment, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.snaps[key]
	if !ok {
		return tree.Segment{}, false
	}
	return tree.SegmentAt(s.Segments, at, s.Idle)
}
func ownerKey(harnessName, callID string) string {
	return fmt.Sprintf("%d:%s%s", len(harnessName), harnessName, callID)
}
func (r *Registry) ToolOwner(harnessName, callID string) (Key, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.owners[ownerKey(harnessName, callID)]
	return k, ok
}
func (r *Registry) ToolCall(key Key, id string) (harness.ToolCall, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.snaps[key]
	if !ok {
		return harness.ToolCall{}, false
	}
	for _, c := range s.Run.ToolCalls {
		if c.ID == id {
			return c, true
		}
	}
	return harness.ToolCall{}, false
}
func (r *Registry) RecordParent(key, parentKey Key, callID string) {
	r.mu.Lock()
	r.parents[key] = parent{parentKey, callID}
	r.mu.Unlock()
}
func (r *Registry) Parent(key Key) (Key, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.parents[key]
	return p.key, p.call, ok
}
func (r *Registry) NoteOwner(key Key, owner string) {
	r.mu.Lock()
	s, ok := r.snaps[key]
	if ok && s.Run.Owner == "" {
		s.Run.Owner = owner
		if s.StartedBy == "" {
			s.StartedBy = s.Run.Harness + ":" + owner
		}
		r.snaps[key] = s
	}
	r.mu.Unlock()
}
func (r *Registry) IncUnresolved()           { r.mu.Lock(); r.unresolved++; r.mu.Unlock() }
func (r *Registry) IncHashKeyMismatch()      { r.mu.Lock(); r.hashMismatch++; r.mu.Unlock() }
func (r *Registry) RecordDropped(key string) { r.mu.Lock(); r.dropped[key]++; r.mu.Unlock() }
func (r *Registry) Resolve(ctx context.Context, key Key, after time.Time) (Snapshot, error) {
	r.mu.Lock()
	if call := r.inflight[key]; call != nil {
		r.mu.Unlock()
		select {
		case <-call.done:
			if call.err != nil {
				return Snapshot{}, call.err
			}
			s, ok := r.Get(key)
			if !ok {
				return Snapshot{}, errors.New("resolved run not found")
			}
			return s, nil
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	if time.Since(r.lastResolve[key]) < time.Second {
		r.mu.Unlock()
		s, ok := r.Get(key)
		if !ok {
			return Snapshot{}, errors.New("run not found")
		}
		return s, nil
	}
	source := r.source
	if source == nil {
		r.mu.Unlock()
		return Snapshot{}, errors.New("registry source is not configured")
	}
	call := &resolveCall{done: make(chan struct{})}
	r.inflight[key] = call
	r.lastResolve[key] = time.Now()
	r.mu.Unlock()
	snapshot, err := source.Resolve(ctx, key, after)
	if err == nil {
		r.Put(snapshot)
	}
	r.mu.Lock()
	delete(r.inflight, key)
	call.err = err
	close(call.done)
	r.mu.Unlock()
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, _ = r.Get(key)
	return snapshot, nil
}
