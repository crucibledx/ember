package ember

import (
	"context"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.uber.org/zap"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

func matchResource(adapters []harness.Adapter, resource pcommon.Resource) (harness.Adapter, bool) {
	for _, adapter := range adapters {
		mapping := adapter.Mapping()
		if len(mapping.Resource) == 0 {
			continue
		}
		matched := true
		for key, accepted := range mapping.Resource {
			value, ok := resource.Attributes().Get(key)
			if !ok || value.Type() != pcommon.ValueTypeStr || !slices.Contains(accepted, value.Str()) {
				matched = false
				break
			}
		}
		if matched {
			return adapter, true
		}
	}
	return nil, false
}

func attribute(attrs, resource pcommon.Map, keys ...string) string {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if value, ok := attrs.Get(key); ok && value.Type() == pcommon.ValueTypeStr && value.Str() != "" {
			return value.Str()
		}
		if value, ok := resource.Get(key); ok && value.Type() == pcommon.ValueTypeStr && value.Str() != "" {
			return value.Str()
		}
	}
	return ""
}

func baseRun(id string) string {
	base, _, _ := strings.Cut(id, "/")
	return base
}
func candidateMatches(session string, key registry.Key) bool {
	return session == "" || baseRun(key.RunID) == session
}

func (p *emberProcessor) resolve(ctx context.Context, key registry.Key, at time.Time) (registry.Snapshot, bool) {
	snapshot, found := p.registry.Get(key)
	if found && (at.IsZero() || !snapshot.LastRecord.IsZero() && !at.After(snapshot.LastRecord)) {
		return snapshot, true
	}
	resolve := p.resolveFn
	if resolve == nil {
		resolve = p.registry.Resolve
	}
	resolved, err := resolve(ctx, key, at)
	if err == nil {
		return resolved, true
	}
	if found {
		return snapshot, true
	}
	p.settings.Logger.Debug("native run is unresolved", zap.String("harness", key.Harness), zap.String("run", key.RunID), zap.Error(err))
	return registry.Snapshot{}, false
}

func (p *emberProcessor) resolveData(ctx context.Context, adapter harness.Adapter, attrs, resource pcommon.Map, at time.Time) (registry.Snapshot, registry.Key, bool) {
	mapping := adapter.Mapping()
	session := attribute(attrs, resource, mapping.SessionKey)
	agent := attribute(attrs, resource, mapping.SubagentKey)
	call := attribute(attrs, resource, mapping.ToolCallKeys...)
	key := registry.Key{Harness: adapter.Name(), RunID: session}
	if agent != "" {
		key.RunID += "/" + agent
	}
	if call != "" && agent == "" {
		if owner, ok := p.registry.ToolOwner(call); ok && owner.Harness == adapter.Name() && candidateMatches(session, owner) {
			if snapshot, ok := p.resolve(ctx, owner, at); ok {
				return snapshot, owner, true
			}
		}
	}
	if key.RunID == "" {
		return registry.Snapshot{}, registry.Key{}, false
	}
	snapshot, ok := p.resolve(ctx, key, at)
	return snapshot, key, ok
}
