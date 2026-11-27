package emit

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/tree"
)

type highWater struct {
	integer int64
	decimal float64
}
type MetricsState struct {
	high map[string]highWater
}

func (s *MetricsState) Metrics(forest *tree.Forest, config Config, now time.Time) pmetric.Metrics {
	out := Metrics(forest, config, now)
	if s.high == nil {
		s.high = map[string]highWater{}
	}
	for _, resource := range out.ResourceMetrics().All() {
		for _, scope := range resource.ScopeMetrics().All() {
			for _, metric := range scope.Metrics().All() {
				s.clampMetric(metric)
			}
		}
	}
	return out
}
func (s *MetricsState) clampMetric(metric pmetric.Metric) {
	if metric.Type() != pmetric.MetricTypeSum {
		return
	}
	points := metric.Sum().DataPoints()
	for i := 0; i < points.Len(); i++ {
		point := points.At(i)
		key := metric.Name() + seriesKey(point.Attributes())
		high := s.high[key]
		if point.ValueType() == pmetric.NumberDataPointValueTypeInt {
			high.integer = max(high.integer, point.IntValue())
			point.SetIntValue(high.integer)
		} else {
			high.decimal = max(high.decimal, point.DoubleValue())
			point.SetDoubleValue(high.decimal)
		}
		s.high[key] = high
	}
}
func seriesKey(attrs pcommon.Map) string { return fmt.Sprintf("%#v", attrs.AsRaw()) }
