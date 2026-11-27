package sessions

import (
	"context"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"

	"github.com/crucibledx/ember/internal/receiver/sessions/internal/metadata"
)

func NewFactory() receiver.Factory {
	receivers := map[component.ID]*sessionReceiver{}
	var mu sync.Mutex
	core := func(id component.ID, cfg *Config) *sessionReceiver {
		mu.Lock()
		defer mu.Unlock()
		if receivers[id] == nil {
			receivers[id] = newSessionReceiver(cfg)
		}
		return receivers[id]
	}
	return receiver.NewFactory(metadata.Type, createDefaultConfig,
		receiver.WithTraces(func(_ context.Context, set receiver.Settings, cfg component.Config, next consumer.Traces) (receiver.Traces, error) {
			r := core(set.ID, cfg.(*Config))
			r.traces = next
			return r, nil
		}, metadata.TracesStability),
		receiver.WithMetrics(func(_ context.Context, set receiver.Settings, cfg component.Config, next consumer.Metrics) (receiver.Metrics, error) {
			r := core(set.ID, cfg.(*Config))
			r.metrics = next
			return r, nil
		}, metadata.MetricsStability),
		receiver.WithLogs(func(_ context.Context, set receiver.Settings, cfg component.Config, next consumer.Logs) (receiver.Logs, error) {
			r := core(set.ID, cfg.(*Config))
			r.logs = next
			return r, nil
		}, metadata.LogsStability))
}
