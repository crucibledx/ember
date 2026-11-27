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
	mu          sync.Mutex
	home        string
	state       *diskState
	metricState emit.MetricsState
	startup     bool
}

func newCore(config *Config, adapters []harness.Adapter) *core {
	if adapters == nil {
		adapters = harness.Adapters()
	}
	return &core{config: config, adapters: adapters, now: time.Now}
}
func (c *core) logger() *zap.Logger { return zap.NewNop() }
func (c *core) start(home string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.home = home
	c.state = loadState(home)
	c.startup = true
	c.restore()
	return c.rescan()
}
func (c *core) restore() {
	for id, s := range c.state.Runs {
		key := registry.Key{Harness: s.Run.Harness, RunID: id}
		for _, call := range s.Tools {
			c.registry.RestoreFile(key, call, registry.SpanRef{})
		}
	}
	for id, at := range c.state.Native {
		if s := c.state.Runs[id]; s != nil {
			c.registry.NoteNative(registry.Key{Harness: s.Run.Harness, RunID: id}, at)
		}
	}
}
func (c *core) rescan() error {
	if c.config.Rescan == 0 {
		return nil
	}
	now := c.now()
	for id, s := range c.state.Runs {
		if now.Sub(tree.LastRecord(s.Run)) <= c.config.Rescan {
			s.LastSegment = 0
			s.Tools = nil
			s.LogTime = time.Time{}
			s.LogOrdinal = 0
			s.FinalMetrics = false
		}
		_ = id
	}
	return nil
}
func (c *core) Poll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == nil {
		c.state = loadState(c.home)
		c.restore()
	}
	now := c.now()
	refs, discoverErr := c.discover()
	var parseErr error
	seen := map[string]bool{}
	for _, item := range refs {
		if seen[item.ref.RunID] {
			continue
		}
		seen[item.ref.RunID] = true
		changed := c.changed(item.ref)
		if !changed {
			continue
		}
		run, err := item.adapter.Parse(os.DirFS(c.home), item.ref)
		if err != nil {
			parseErr = err
			continue
		}
		state := c.state.Runs[run.SessionID]
		if state == nil {
			state = &runState{}
			c.state.Runs[run.SessionID] = state
		}
		state.Files, state.Run = c.fileStates(item.ref), run
	}
	c.applyParents()
	forest := c.forest(now)
	c.publish(forest)
	var consumeErr error
	if c.traces != nil {
		consumeErr = c.emitTraces(ctx, forest, now)
	}
	if c.logs != nil {
		if err := c.emitLogs(ctx, forest, now); err != nil {
			consumeErr = err
		}
	}
	if c.metrics != nil {
		if err := c.emitMetrics(ctx, forest, now); err != nil {
			consumeErr = err
		}
	}
	for id, state := range c.state.Runs {
		if at, ok := c.registry.NativeFirst(registry.Key{Harness: state.Run.Harness, RunID: id}); ok {
			c.state.Native[id] = at
		}
	}
	saveErr := saveState(c.home, c.state)
	return errors.Join(discoverErr, parseErr, consumeErr, saveErr)
}

type discovered struct {
	adapter harness.Adapter
	ref     harness.SessionRef
}

func (c *core) discover() ([]discovered, error) {
	var out []discovered
	var errs []error
	env := harness.Env{Home: c.home, FS: os.DirFS(c.home), OS: runtime.GOOS}
	for _, adapter := range c.adapters {
		refs, err := adapter.Sessions(env)
		if err != nil {
			errs = append(errs, err)
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
		if info, err := fs.Stat(os.DirFS(c.home), name); err == nil {
			out[name] = fileState{Size: info.Size(), ModTime: info.ModTime()}
		}
	}
	return out
}
func (c *core) changed(ref harness.SessionRef) bool {
	old := c.state.Runs[ref.RunID]
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
	for id, s := range c.state.Runs {
		key := registry.Key{Harness: s.Run.Harness, RunID: id}
		if parent, call, ok := c.registry.Parent(key); ok {
			s.Run.Parent = &harness.ParentRef{SessionID: parent.RunID, ToolCallID: call}
			if s.Run.Harness == "" {
				s.Run.Harness = parent.Harness
			}
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
		c.registry.Put(registry.Snapshot{Run: node.Run, TraceID: node.TraceID(), RootKey: node.RootKey, RootSessionID: node.RootSessionID, WorkID: node.WorkID, Segments: node.Segments, ToolSpans: spans, Idle: node.Idle, LastRecord: tree.LastRecord(node.Run)})
	}
}
func (c *core) Resolve(ctx context.Context, key registry.Key, after time.Time) (harness.Run, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state.Runs[key.RunID]
	if s != nil && !after.IsZero() && !tree.LastRecord(s.Run).Before(after) {
		return s.Run, nil
	}
	for _, a := range c.adapters {
		if a.Name() != key.Harness {
			continue
		}
		refs, err := a.Sessions(harness.Env{Home: c.home, FS: os.DirFS(c.home), OS: runtime.GOOS})
		if err != nil {
			return harness.Run{}, err
		}
		for _, ref := range refs {
			if ref.RunID != key.RunID {
				continue
			}
			run, err := a.Parse(os.DirFS(c.home), ref)
			if err == nil {
				state := c.state.Runs[key.RunID]
				if state == nil {
					state = &runState{}
					c.state.Runs[key.RunID] = state
				}
				state.Files, state.Run = c.fileStates(ref), run
			}
			return run, err
		}
	}
	return harness.Run{}, fmt.Errorf("session %s not found", key.RunID)
}
func recent(run harness.Run, now time.Time, lookback time.Duration) bool {
	return !now.After(tree.LastRecord(run).Add(lookback))
}
func sortedTools(in []string) []string {
	out := append([]string(nil), in...)
	slices.Sort(out)
	return out
}
