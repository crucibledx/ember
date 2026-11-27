package sessions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/collector/consumer"
	"go.uber.org/zap"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

type core struct {
	config      *Config
	adapters    []harness.Adapter
	registry    *registry.Registry
	traces      consumer.Traces
	metrics     consumer.Metrics
	logs        consumer.Logs
	now         func() time.Time
	log         *zap.Logger
	mu          sync.Mutex
	home        string
	userHome    string
	state       *diskState
	metricState emit.MetricsState
	startup     bool
}

func newCore(config *Config, adapters []harness.Adapter) *core {
	if adapters == nil {
		adapters = harness.Adapters()
	}
	return &core{config: config, adapters: adapters, now: time.Now, log: zap.NewNop()}
}
func (c *core) logger() *zap.Logger { return c.log }
func (c *core) start(home string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.home = home
	state, err := loadState(home)
	if err != nil {
		return err
	}
	if c.userHome == "" {
		c.userHome = home
	}
	c.state = state
	c.startup = true
	c.restore()
	c.rescan()
	return nil
}
func (c *core) restore() {
	for id, s := range c.state.Runs {
		key := registry.Key{Harness: s.Run.Harness, RunID: s.Run.SessionID}
		for _, call := range s.Tools {
			c.registry.RestoreFile(key, call, registry.SpanRef{})
		}
		for call, nativeID := range s.NativeTools {
			c.registry.RestoreNativeTool(key, call, nativeID)
		}
		if at, ok := c.state.Native[id]; ok {
			c.registry.RestoreNativeFirst(key, at)
		}
		if at, ok := c.state.NativeLast[id]; ok {
			c.registry.RestoreNativeLast(key, at)
		}
	}
}
func (c *core) rescan() {
	if c.config.Rescan == 0 {
		return
	}
	now := c.now()
	for _, s := range c.state.Runs {
		if now.Sub(tree.LastRecord(s.Run)) <= c.config.Rescan {
			s.LastSegment = 0
			s.EmittedSegments = nil
			s.Tools = nil
			s.LogTime = time.Time{}
			s.LogOrdinal = 0
			s.AcceptedLogs = nil
			s.FinalMetrics = false
		}
	}
}
func (c *core) lookback() time.Duration { return max(c.config.Lookback, c.config.Rescan) }
func (c *core) Poll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == nil {
		state, err := loadState(c.home)
		if err != nil {
			return err
		}
		c.state = state
		c.restore()
	}
	now := c.now()
	refs, discoverErr := c.discover()
	errs := []error{discoverErr}
	seen := map[string]bool{}
	for _, item := range refs {
		key := stateKey(item.adapter.Name(), item.ref.RunID)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !c.changed(item.adapter, item.ref) {
			continue
		}
		run, err := item.adapter.Parse(os.DirFS(c.userHome), item.ref)
		if err != nil {
			errs = append(errs, fmt.Errorf("parse %s session %s: %w", item.adapter.Name(), item.ref.RunID, err))
			continue
		}
		state := c.state.Runs[stateKey(run.Harness, run.SessionID)]
		if state == nil {
			state = &runState{}
			c.state.Runs[stateKey(run.Harness, run.SessionID)] = state
		}
		state.Files, state.Run = c.fileStates(item.ref), run
	}
	c.applyParents()
	forest := c.forest(now)
	c.publish(forest)
	if c.traces != nil {
		errs = append(errs, c.emitTraces(ctx, forest, now))
	}
	if c.logs != nil {
		errs = append(errs, c.emitLogs(ctx, forest, now))
	}
	if c.metrics != nil {
		errs = append(errs, c.emitMetrics(ctx, forest, now))
	}
	for id, state := range c.state.Runs {
		key := registry.Key{Harness: state.Run.Harness, RunID: state.Run.SessionID}
		if at, ok := c.registry.NativeFirst(key); ok {
			c.state.Native[id] = at
		}
		if at, ok := c.registry.NativeLast(key); ok {
			c.state.NativeLast[id] = at
		}
		state.NativeTools = c.registry.NativeCalls(key)
	}
	errs = append(errs, saveState(c.home, c.state))
	return errors.Join(errs...)
}

type discovered struct {
	adapter harness.Adapter
	ref     harness.SessionRef
}

func (c *core) discover() ([]discovered, error) {
	var out []discovered
	var errs []error
	env := harness.Env{Home: c.userHome, FS: os.DirFS(c.userHome), OS: runtime.GOOS}
	for _, adapter := range c.adapters {
		refs, err := adapter.Sessions(env)
		if err != nil {
			errs = append(errs, fmt.Errorf("discover %s sessions: %w", adapter.Name(), err))
			continue
		}
		for _, ref := range refs {
			out = append(out, discovered{adapter, ref})
		}
	}
	return out, errors.Join(errs...)
}
func (c *core) fileStates(ref harness.SessionRef) map[string]fileState {
	out := map[string]fileState{}
	for _, name := range ref.Files {
		if info, err := fs.Stat(os.DirFS(c.userHome), name); err == nil {
			out[name] = fileState{Size: info.Size(), ModTime: info.ModTime()}
		}
	}
	return out
}
func (c *core) changed(adapter harness.Adapter, ref harness.SessionRef) bool {
	old := c.state.Runs[stateKey(adapter.Name(), ref.RunID)]
	if old == nil {
		return true
	}
	current := c.fileStates(ref)
	if len(current) != len(old.Files) {
		return true
	}
	for name, info := range current {
		if old.Files[name] != info {
			return true
		}
	}
	return false
}
func (c *core) applyParents() {
	for _, s := range c.state.Runs {
		key := registry.Key{Harness: s.Run.Harness, RunID: s.Run.SessionID}
		if parent, call, ok := c.registry.Parent(key); ok {
			s.Run.Parent = &harness.ParentRef{SessionID: parent.RunID, ToolCallID: call}
		}
	}
}
func (c *core) forest(now time.Time) *tree.Forest {
	runs := make([]harness.Run, 0, len(c.state.Runs))
	for _, s := range c.state.Runs {
		runs = append(runs, s.Run)
	}
	return tree.BuildForest(runs, tree.Options{Now: now, Idle: c.config.Idle, Roots: &c.state.Roots})
}
func (c *core) publish(forest *tree.Forest) {
	for _, node := range forest.Ordered() {
		if node.Held {
			continue
		}
		spans := map[string]registry.SpanRef{}
		for _, call := range node.Run.ToolCalls {
			spans[call.ID] = registry.SpanRef{TraceID: node.TraceID(), SpanID: tree.ToolSpanID(node.Run.Harness, call.ID)}
		}
		c.registry.Put(registry.Snapshot{Run: node.Run, TraceID: node.TraceID(), RootKey: node.RootKey, RootSessionID: node.RootSessionID, WorkID: node.WorkID, WorkstreamID: node.WorkstreamID, Segments: node.Segments, ToolSpans: spans, Idle: node.Idle, LastRecord: tree.LastRecord(node.Run), StartedBy: node.StartedBy, Active: append([]string(nil), node.Active...)})
	}
}
func (c *core) Resolve(ctx context.Context, key registry.Key, after time.Time) (registry.Snapshot, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	id := stateKey(key.Harness, key.RunID)
	s := c.state.Runs[id]
	if s == nil || (!after.IsZero() && tree.LastRecord(s.Run).Before(after)) {
		if err := c.resolveRun(key); err != nil {
			return registry.Snapshot{}, err
		}
	}
	c.applyParents()
	c.publish(c.forest(c.now()))
	if err := saveState(c.home, c.state); err != nil {
		return registry.Snapshot{}, err
	}
	snapshot, ok := c.registry.Get(key)
	if !ok {
		return registry.Snapshot{}, fmt.Errorf("session %s is held or unavailable", key.RunID)
	}
	return snapshot, nil
}
func (c *core) resolveRun(key registry.Key) error {
	for _, adapter := range c.adapters {
		if adapter.Name() != key.Harness {
			continue
		}
		refs, err := adapter.Sessions(harness.Env{Home: c.userHome, FS: os.DirFS(c.userHome), OS: runtime.GOOS})
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref.RunID != key.RunID {
				continue
			}
			run, err := adapter.Parse(os.DirFS(c.userHome), ref)
			if err != nil {
				return err
			}
			state := c.state.Runs[stateKey(run.Harness, run.SessionID)]
			if state == nil {
				state = &runState{}
				c.state.Runs[stateKey(run.Harness, run.SessionID)] = state
			}
			state.Files, state.Run = c.fileStates(ref), run
			return nil
		}
	}
	return fmt.Errorf("session %s not found", key.RunID)
}
func recent(run harness.Run, now time.Time, lookback time.Duration) bool {
	return !now.After(tree.LastRecord(run).Add(lookback))
}
func sortedTools(in []string) []string {
	out := append([]string(nil), in...)
	slices.Sort(out)
	return out
}
