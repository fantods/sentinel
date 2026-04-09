package telemetry

import (
	"go.opentelemetry.io/otel/metric"
)

type Instruments struct {
	RequestDuration metric.Float64Histogram
	TTFT            metric.Float64Histogram
	TPS             metric.Float64Histogram
	TokensTotal     metric.Int64Counter
	CostDollars     metric.Float64Counter
	RequestsTotal   metric.Int64Counter
	CacheResults    metric.Int64Counter
	ErrorsTotal     metric.Int64Counter
	ActiveRequests  metric.Int64UpDownCounter
}

func newInstruments(meter metric.Meter) (*Instruments, error) {
	var errs []error
	ins := &Instruments{}

	ins.RequestDuration, _ = meter.Float64Histogram("llm_request_duration_seconds",
		metric.WithDescription("Total request wall-clock time"),
		metric.WithUnit("s"),
	)

	ins.TTFT, _ = meter.Float64Histogram("llm_ttft_seconds",
		metric.WithDescription("Time from request received to first token sent to client"),
		metric.WithUnit("s"),
	)

	ins.TPS, _ = meter.Float64Histogram("llm_tokens_per_second",
		metric.WithDescription("Output token streaming speed"),
		metric.WithUnit("tok/s"),
	)

	ins.TokensTotal, _ = meter.Int64Counter("llm_tokens_total",
		metric.WithDescription("Total token consumption"),
		metric.WithUnit("{tokens}"),
	)

	ins.CostDollars, _ = meter.Float64Counter("llm_cost_dollars",
		metric.WithDescription("Running cost attribution"),
		metric.WithUnit("USD"),
	)

	ins.RequestsTotal, _ = meter.Int64Counter("llm_requests_total",
		metric.WithDescription("Total request count"),
		metric.WithUnit("{req}"),
	)

	ins.CacheResults, _ = meter.Int64Counter("llm_cache_result_total",
		metric.WithDescription("Cache hit/miss count"),
		metric.WithUnit("{result}"),
	)

	ins.ErrorsTotal, _ = meter.Int64Counter("llm_errors_total",
		metric.WithDescription("Errors by taxonomy"),
		metric.WithUnit("{err}"),
	)

	ins.ActiveRequests, _ = meter.Int64UpDownCounter("llm_active_requests",
		metric.WithDescription("Concurrent in-flight requests"),
		metric.WithUnit("{req}"),
	)

	if len(errs) > 0 {
		return nil, errs[0]
	}
	return ins, nil
}
