package ember

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/privacy"
)

func (p *emberProcessor) policy(adapter harness.Adapter, matched bool) (*privacy.Policy, error) {
	config := privacy.Config{Content: p.config.Privacy.Content, Allow: p.config.Privacy.Allow,
		Deny: p.config.Privacy.Deny, HumanIdentity: p.config.Privacy.HumanIdentity, HashKey: p.config.Privacy.HashKey}
	if matched {
		mapping := adapter.Mapping()
		config.NativeAllow = mapping.NativeAllow
		config.ContentKeys = mapping.ContentKeys
		config.OwnerKeys = mapping.OwnerKeys
	}
	return privacy.New(config)
}

func (p *emberProcessor) filter(policy *privacy.Policy, attrs pcommon.Map) {
	for key, count := range policy.Filter(attrs) {
		for range count {
			p.registry.RecordDropped(key)
		}
	}
}

func (p *emberProcessor) privacyResource(resource pcommon.Resource, policy *privacy.Policy) {
	if id := p.hashKeyID(); id != "" {
		resource.Attributes().PutStr("ember.hash_key.id", id)
	}
	p.filter(policy, resource.Attributes())
}

func (p *emberProcessor) privacyTraces(resource ptrace.ResourceSpans, adapter harness.Adapter, matched bool) error {
	policy, err := p.policy(adapter, matched)
	if err != nil {
		return err
	}
	p.privacyResource(resource.Resource(), policy)
	for i := 0; i < resource.ScopeSpans().Len(); i++ {
		scope := resource.ScopeSpans().At(i)
		p.filter(policy, scope.Scope().Attributes())
		for j := 0; j < scope.Spans().Len(); j++ {
			span := scope.Spans().At(j)
			p.filter(policy, span.Attributes())
			for k := 0; k < span.Events().Len(); k++ {
				p.filter(policy, span.Events().At(k).Attributes())
			}
			for k := 0; k < span.Links().Len(); k++ {
				p.filter(policy, span.Links().At(k).Attributes())
			}
			span.Status().SetMessage(policy.FilterStatusMessage(span.Status().Message()))
		}
	}
	return nil
}

func (p *emberProcessor) privacyLogs(resource plog.ResourceLogs, adapter harness.Adapter, matched bool) error {
	policy, err := p.policy(adapter, matched)
	if err != nil {
		return err
	}
	p.privacyResource(resource.Resource(), policy)
	for i := 0; i < resource.ScopeLogs().Len(); i++ {
		scope := resource.ScopeLogs().At(i)
		p.filter(policy, scope.Scope().Attributes())
		for j := 0; j < scope.LogRecords().Len(); j++ {
			record := scope.LogRecords().At(j)
			p.filter(policy, record.Attributes())
			policy.FilterLogBody(record.Body(), record.Attributes())
		}
	}
	return nil
}

func (p *emberProcessor) privacyMetrics(resource pmetric.ResourceMetrics, adapter harness.Adapter, matched bool) error {
	policy, err := p.policy(adapter, matched)
	if err != nil {
		return err
	}
	p.privacyResource(resource.Resource(), policy)
	for i := 0; i < resource.ScopeMetrics().Len(); i++ {
		scope := resource.ScopeMetrics().At(i)
		p.filter(policy, scope.Scope().Attributes())
		for j := 0; j < scope.Metrics().Len(); j++ {
			metric := scope.Metrics().At(j)
			visitPoints(metric, func(attrs pcommon.Map, _ time.Time) { p.filter(policy, attrs) })
			visitExemplars(metric, func(attrs pcommon.Map) { p.filter(policy, attrs) })
		}
	}
	return nil
}

func visitExemplars(metric pmetric.Metric, visit func(pcommon.Map)) {
	visitSlice := func(exemplars pmetric.ExemplarSlice) {
		for i := 0; i < exemplars.Len(); i++ {
			visit(exemplars.At(i).FilteredAttributes())
		}
	}
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		for i := 0; i < metric.Gauge().DataPoints().Len(); i++ {
			visitSlice(metric.Gauge().DataPoints().At(i).Exemplars())
		}
	case pmetric.MetricTypeSum:
		for i := 0; i < metric.Sum().DataPoints().Len(); i++ {
			visitSlice(metric.Sum().DataPoints().At(i).Exemplars())
		}
	case pmetric.MetricTypeHistogram:
		for i := 0; i < metric.Histogram().DataPoints().Len(); i++ {
			visitSlice(metric.Histogram().DataPoints().At(i).Exemplars())
		}
	case pmetric.MetricTypeExponentialHistogram:
		for i := 0; i < metric.ExponentialHistogram().DataPoints().Len(); i++ {
			visitSlice(metric.ExponentialHistogram().DataPoints().At(i).Exemplars())
		}
	case pmetric.MetricTypeSummary, pmetric.MetricTypeEmpty:
	}
}
