package ember

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

const remapTTL = time.Hour

type emberProcessor struct {
	config    *Config
	settings  processor.Settings
	adapters  []harness.Adapter
	registry  *registry.Registry
	cache     *remapCache
	hashID    string
	hashOnce  sync.Once
	resolveFn func(context.Context, registry.Key, time.Time) (registry.Snapshot, error)

	nextTraces  consumer.Traces
	nextMetrics consumer.Metrics
	nextLogs    consumer.Logs
	startOnce   sync.Once
	startErr    error
}

func (p *emberProcessor) Start(_ context.Context, host component.Host) error {
	p.startOnce.Do(func() {
		if p.registry == nil {
			p.registry, p.startErr = registry.FromHost(host, p.config.Registry)
		}
	})
	return p.startErr
}
func (p *emberProcessor) Shutdown(context.Context) error { return nil }
func (p *emberProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *emberProcessor) ConsumeTraces(ctx context.Context, traces ptrace.Traces) error {
	if err := p.processTraces(ctx, traces); err != nil {
		return err
	}
	return p.nextTraces.ConsumeTraces(ctx, traces)
}
func (p *emberProcessor) ConsumeMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	if err := p.processMetrics(ctx, metrics); err != nil {
		return err
	}
	return p.nextMetrics.ConsumeMetrics(ctx, metrics)
}
func (p *emberProcessor) ConsumeLogs(ctx context.Context, logs plog.Logs) error {
	if err := p.processLogs(ctx, logs); err != nil {
		return err
	}
	return p.nextLogs.ConsumeLogs(ctx, logs)
}

func (p *emberProcessor) hashKeyID() string {
	if p.config.Privacy.HashKeyID != "" {
		return p.config.Privacy.HashKeyID
	}
	p.hashOnce.Do(func() {
		if p.config.Privacy.HashKey != "" {
			sum := sha256.Sum256([]byte(p.config.Privacy.HashKey))
			p.hashID = hex.EncodeToString(sum[:])[:8]
		}
	})
	return p.hashID
}
