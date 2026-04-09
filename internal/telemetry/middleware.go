package telemetry

import (
	"context"
	"net/http"

	"github.com/mattemmons/sentinel/internal/middleware"
	"github.com/mattemmons/sentinel/internal/pricing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type streamCtxKey struct{}

type TelemetryMiddleware struct {
	instruments *Instruments
}

func NewTelemetryMiddleware(instruments *Instruments) *TelemetryMiddleware {
	return &TelemetryMiddleware{instruments: instruments}
}

func ContextWithStreamTelemetry(ctx context.Context, st *StreamTelemetry) context.Context {
	return context.WithValue(ctx, streamCtxKey{}, st)
}

func StreamTelemetryFromContext(ctx context.Context) *StreamTelemetry {
	if st, ok := ctx.Value(streamCtxKey{}).(*StreamTelemetry); ok {
		return st
	}
	return nil
}

type statusWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.written {
		sw.statusCode = code
		sw.written = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.written {
		sw.statusCode = http.StatusOK
		sw.written = true
	}
	return sw.ResponseWriter.Write(b)
}

func (tm *TelemetryMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tenant := middleware.TenantFromContext(ctx)

		tm.instruments.ActiveRequests.Add(ctx, 1,
			metric.WithAttributes(attribute.String("tenant", tenant)),
		)

		sw := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(sw, r)

		tm.instruments.ActiveRequests.Add(ctx, -1,
			metric.WithAttributes(attribute.String("tenant", tenant)),
		)

		st := StreamTelemetryFromContext(ctx)
		if st != nil {
			st.Tenant = tenant
			tm.recordStream(st, sw.statusCode)
		}
	})
}

func (tm *TelemetryMiddleware) recordStream(st *StreamTelemetry, statusCode int) {
	ctx := context.Background()

	status := "success"
	if statusCode >= 400 {
		status = "error"
	}

	reqAttrs := metric.WithAttributes(
		attribute.String("model", st.Model),
		attribute.String("provider", st.Provider),
		attribute.String("status", status),
	)

	tm.instruments.RequestDuration.Record(ctx, st.Duration(), reqAttrs)
	tm.instruments.RequestsTotal.Add(ctx, 1, reqAttrs)

	if ttft := st.TTFT(); ttft > 0 {
		tm.instruments.TTFT.Record(ctx, ttft,
			metric.WithAttributes(
				attribute.String("model", st.Model),
				attribute.String("provider", st.Provider),
			),
		)
	}

	if tps := st.TPS(); tps > 0 {
		tm.instruments.TPS.Record(ctx, tps,
			metric.WithAttributes(
				attribute.String("model", st.Model),
				attribute.String("provider", st.Provider),
			),
		)
	}

	if st.InputTokens > 0 {
		tm.instruments.TokensTotal.Add(ctx, int64(st.InputTokens),
			metric.WithAttributes(
				attribute.String("model", st.Model),
				attribute.String("type", "input"),
			),
		)
	}

	if st.OutputTokens > 0 {
		tm.instruments.TokensTotal.Add(ctx, int64(st.OutputTokens),
			metric.WithAttributes(
				attribute.String("model", st.Model),
				attribute.String("type", "output"),
			),
		)
	}

	cost := pricing.CalculateCost(st.Model, st.InputTokens, st.OutputTokens)
	if cost > 0 {
		tm.instruments.CostDollars.Add(ctx, cost,
			metric.WithAttributes(
				attribute.String("model", st.Model),
				attribute.String("tenant", st.Tenant),
			),
		)
	}
}
