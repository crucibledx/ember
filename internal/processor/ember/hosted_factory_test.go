package ember

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/crucibledx/ember/internal/extension/registry"
)

func hostedFactoryConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Privacy.HashKey = "factory-isolation-test"
	return cfg
}

func TestFactoryCreatesPipelineLocalProcessors(t *testing.T) {
	ctx := context.Background()
	factory := NewFactory()
	settings := processortest.NewNopSettings(factory.Type())

	t.Run("traces", func(t *testing.T) {
		firstSink := new(consumertest.TracesSink)
		secondSink := new(consumertest.TracesSink)
		first, err := factory.CreateTraces(ctx, settings, hostedFactoryConfig(), firstSink)
		require.NoError(t, err)
		second, err := factory.CreateTraces(ctx, settings, hostedFactoryConfig(), secondSink)
		require.NoError(t, err)
		require.NotSame(t, first, second)
		first.(*emberProcessor).registry = registry.New()
		second.(*emberProcessor).registry = registry.New()

		require.NoError(t, first.ConsumeTraces(ctx, generateLifecycleTestTraces()))
		require.NoError(t, second.ConsumeTraces(ctx, generateLifecycleTestTraces()))
		require.Len(t, firstSink.AllTraces(), 1)
		require.Len(t, secondSink.AllTraces(), 1)
	})

	t.Run("metrics", func(t *testing.T) {
		firstSink := new(consumertest.MetricsSink)
		secondSink := new(consumertest.MetricsSink)
		first, err := factory.CreateMetrics(ctx, settings, hostedFactoryConfig(), firstSink)
		require.NoError(t, err)
		second, err := factory.CreateMetrics(ctx, settings, hostedFactoryConfig(), secondSink)
		require.NoError(t, err)
		require.NotSame(t, first, second)
		first.(*emberProcessor).registry = registry.New()
		second.(*emberProcessor).registry = registry.New()

		require.NoError(t, first.ConsumeMetrics(ctx, generateLifecycleTestMetrics()))
		require.NoError(t, second.ConsumeMetrics(ctx, generateLifecycleTestMetrics()))
		require.Len(t, firstSink.AllMetrics(), 1)
		require.Len(t, secondSink.AllMetrics(), 1)
	})

	t.Run("logs", func(t *testing.T) {
		firstSink := new(consumertest.LogsSink)
		secondSink := new(consumertest.LogsSink)
		first, err := factory.CreateLogs(ctx, settings, hostedFactoryConfig(), firstSink)
		require.NoError(t, err)
		second, err := factory.CreateLogs(ctx, settings, hostedFactoryConfig(), secondSink)
		require.NoError(t, err)
		require.NotSame(t, first, second)
		first.(*emberProcessor).registry = registry.New()
		second.(*emberProcessor).registry = registry.New()

		require.NoError(t, first.ConsumeLogs(ctx, generateLifecycleTestLogs()))
		require.NoError(t, second.ConsumeLogs(ctx, generateLifecycleTestLogs()))
		require.Len(t, firstSink.AllLogs(), 1)
		require.Len(t, secondSink.AllLogs(), 1)
	})
}
