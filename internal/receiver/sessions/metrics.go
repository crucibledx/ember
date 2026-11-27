package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/tree"
)

func metricResourceKey(resource pmetric.ResourceMetrics) string {
	runID, _ := resource.Resource().Attributes().Get("service.instance.id")
	for _, scope := range resource.ScopeMetrics().All() {
		for _, metric := range scope.Metrics().All() {
			points := pmetric.NewNumberDataPointSlice()
			switch metric.Type() {
			case pmetric.MetricTypeGauge:
				points = metric.Gauge().DataPoints()
			case pmetric.MetricTypeSum:
				points = metric.Sum().DataPoints()
			}
			if points.Len() > 0 {
				harnessName, _ := points.At(0).Attributes().Get("ember.harness")
				return stateKey(harnessName.Str(), runID.Str())
			}
		}
	}
	return ""
}
func (c *core) emitMetrics(ctx context.Context, forest *tree.Forest, now time.Time) error {
	out := c.metricState.Metrics(forest, emit.Config{}, now)
	signatures := map[string]string{}
	for _, resource := range out.ResourceMetrics().All() {
		id := metricResourceKey(resource)
		state := c.state.Runs[id]
		if state == nil {
			continue
		}
		if !tree.Terminal(state.Run.Status) && !tree.RunClosed(state.Run, now, c.config.Idle) {
			state.invalidateFinalMetrics()
			continue
		}
		signature, err := metricSignature(resource)
		if err != nil {
			return fmt.Errorf("checkpoint metrics: %w", err)
		}
		signatures[id] = signature
		if state.FinalMetrics {
			if state.FinalMetricSignature == "" {
				state.FinalMetricSignature = signature
			} else if state.FinalMetricSignature != signature {
				state.invalidateFinalMetrics()
			}
		}
	}
	due := map[string]bool{}
	for _, node := range forest.Ordered() {
		if node.Held {
			continue
		}
		id := stateKey(node.Run.Harness, node.Run.SessionID)
		state := c.state.Runs[id]
		due[id] = emit.MetricDue(node.Run, now, c.config.Idle, c.lookback(), c.startup, state.FinalMetrics)
	}
	out.ResourceMetrics().RemoveIf(func(r pmetric.ResourceMetrics) bool { return !due[metricResourceKey(r)] })
	if out.MetricCount() == 0 {
		return nil
	}
	checkpoint := pmetric.NewMetrics()
	out.CopyTo(checkpoint)
	err := c.metrics.ConsumeMetrics(ctx, out)
	failed := map[string]bool{}
	if err != nil {
		var partial consumererror.Metrics
		if errors.As(err, &partial) {
			for _, resource := range partial.Data().ResourceMetrics().All() {
				id := metricResourceKey(resource)
				if !due[id] {
					for dueID := range due {
						failed[dueID] = true
					}
					break
				}
				failed[id] = true
			}
		} else {
			for id := range due {
				failed[id] = true
			}
		}
	}
	for _, resource := range checkpoint.ResourceMetrics().All() {
		id := metricResourceKey(resource)
		if !due[id] {
			continue
		}
		state := c.state.Runs[id]
		if failed[id] {
			state.invalidateFinalMetrics()
			continue
		}
		if tree.Terminal(state.Run.Status) || tree.RunClosed(state.Run, now, c.config.Idle) {
			state.FinalMetrics = true
			state.FinalMetricSignature = signatures[id]
		}
	}
	c.startup = false
	return err
}

func metricSignature(resource pmetric.ResourceMetrics) (string, error) {
	out := pmetric.NewMetrics()
	resource.CopyTo(out.ResourceMetrics().AppendEmpty())
	for _, resource := range out.ResourceMetrics().All() {
		for _, scope := range resource.ScopeMetrics().All() {
			for _, metric := range scope.Metrics().All() {
				switch metric.Type() {
				case pmetric.MetricTypeGauge:
					zeroNumberTimestamps(metric.Gauge().DataPoints())
				case pmetric.MetricTypeSum:
					zeroNumberTimestamps(metric.Sum().DataPoints())
				case pmetric.MetricTypeHistogram:
					for _, point := range metric.Histogram().DataPoints().All() {
						point.SetTimestamp(0)
					}
				case pmetric.MetricTypeExponentialHistogram:
					for _, point := range metric.ExponentialHistogram().DataPoints().All() {
						point.SetTimestamp(0)
					}
				case pmetric.MetricTypeSummary:
					for _, point := range metric.Summary().DataPoints().All() {
						point.SetTimestamp(0)
					}
				case pmetric.MetricTypeEmpty:
				}
			}
		}
	}
	body, err := new(pmetric.ProtoMarshaler).MarshalMetrics(out)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
func zeroNumberTimestamps(points pmetric.NumberDataPointSlice) {
	for _, point := range points.All() {
		point.SetTimestamp(0)
	}
}
func (s *runState) invalidateFinalMetrics() {
	s.FinalMetrics = false
	s.FinalMetricSignature = ""
}
