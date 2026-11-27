// Package ember implements the Ember native telemetry processor.
package ember

import (
	"context"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/processor/ember/internal/metadata"
)

func NewFactory() processor.Factory {
	var mu sync.Mutex
	processors := map[component.ID]*emberProcessor{}
	core := func(id component.ID, cfg *Config, set processor.Settings) *emberProcessor {
		mu.Lock()
		defer mu.Unlock()
		if processors[id] == nil {
			processors[id] = newProcessor(cfg, set)
		}
		return processors[id]
	}
	return processor.NewFactory(metadata.Type, createDefaultConfig,
		processor.WithTraces(func(_ context.Context, set processor.Settings, cfg component.Config, next consumer.Traces) (processor.Traces, error) {
			p := core(set.ID, cfg.(*Config), set)
			p.nextTraces = next
			return p, nil
		}, metadata.TracesStability),
		processor.WithMetrics(func(_ context.Context, set processor.Settings, cfg component.Config, next consumer.Metrics) (processor.Metrics, error) {
			p := core(set.ID, cfg.(*Config), set)
			p.nextMetrics = next
			return p, nil
		}, metadata.MetricsStability),
		processor.WithLogs(func(_ context.Context, set processor.Settings, cfg component.Config, next consumer.Logs) (processor.Logs, error) {
			p := core(set.ID, cfg.(*Config), set)
			p.nextLogs = next
			return p, nil
		}, metadata.LogsStability))
}

func newProcessor(cfg *Config, set processor.Settings) *emberProcessor {
	return &emberProcessor{config: cfg, settings: set, adapters: harness.Adapters(), cache: newRemapCache(100000, remapTTL)}
}
