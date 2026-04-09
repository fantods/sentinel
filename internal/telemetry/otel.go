package telemetry

import (
	"context"
	"log/slog"
	"net/http"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Manager struct {
	meter       metric.Meter
	provider    *sdkmetric.MeterProvider
	exporter    *prometheus.Exporter
	registry    *promclient.Registry
	instruments *Instruments
	logger      *slog.Logger
}

func NewManager(serviceName, version string) (*Manager, error) {
	registry := promclient.NewRegistry()

	exporter, err := prometheus.New(prometheus.WithRegisterer(registry))
	if err != nil {
		return nil, err
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
		semconv.ServiceVersionKey.String(version),
	)

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(exporter),
	)
	otel.SetMeterProvider(provider)

	meter := provider.Meter("github.com/mattemmons/sentinel")

	instruments, err := newInstruments(meter)
	if err != nil {
		return nil, err
	}

	return &Manager{
		meter:       meter,
		provider:    provider,
		exporter:    exporter,
		registry:    registry,
		instruments: instruments,
		logger:      slog.Default(),
	}, nil
}

func (m *Manager) Instruments() *Instruments {
	return m.instruments
}

func (m *Manager) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Manager) Shutdown(ctx context.Context) error {
	return m.provider.Shutdown(ctx)
}
