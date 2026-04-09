package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/mattemmons/sentinel/internal/auth"
	"github.com/mattemmons/sentinel/internal/config"
	"github.com/mattemmons/sentinel/internal/middleware"
	"github.com/mattemmons/sentinel/internal/proxy"
	"github.com/mattemmons/sentinel/internal/telemetry"
)

type Server struct {
	cfg    *config.Config
	logger *slog.Logger
	http   *http.Server
	tel    *telemetry.Manager
}

func New(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	s := &Server{
		cfg:    cfg,
		logger: logger,
	}

	var tel *telemetry.Manager
	if cfg.TelemetryEnabled {
		var err error
		tel, err = telemetry.NewManager("sentinel", "0.1.0")
		if err != nil {
			return nil, err
		}
		s.tel = tel
	}

	keyStore := auth.NewKeyStore(cfg.APIKeys)
	proxyHandler := proxy.NewHandler(cfg.UpstreamBaseURL, cfg.UpstreamAPIKey, cfg.MaxRetries, cfg.RetryBaseDelay, logger, tel)
	rateLimiter := middleware.NewRateLimiter(cfg.RateLimitRPS)

	mux := http.NewServeMux()

	inner := http.Handler(proxyHandler)

	if cfg.TelemetryEnabled && tel != nil {
		tm := telemetry.NewTelemetryMiddleware(tel.Instruments())
		inner = tm.Wrap(inner)
	}

	proxyChain := middleware.Recovery(logger)(
		middleware.Auth(keyStore, logger)(
			rateLimiter.Middleware()(inner),
		),
	)
	mux.Handle("/v1/", proxyChain)
	mux.Handle("/v4/", proxyChain)

	if cfg.TelemetryEnabled && tel != nil {
		mux.Handle(cfg.TelemetryPath, tel.Handler())
	}

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	s.http = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	return s, nil
}

func (s *Server) Start() error {
	s.logger.Info("starting sentinel gateway",
		"addr", s.cfg.ListenAddr,
		"upstream", s.cfg.UpstreamBaseURL,
		"telemetry", s.cfg.TelemetryEnabled,
	)
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down sentinel gateway")
	var errs []error
	if err := s.http.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if s.tel != nil {
		if err := s.tel.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
