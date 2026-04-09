package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr        string            `yaml:"listen_addr" json:"listen_addr"`
	UpstreamBaseURL   string            `yaml:"upstream_base_url" json:"upstream_base_url"`
	UpstreamAPIKey    string            `yaml:"upstream_api_key" json:"upstream_api_key"`
	APIKeys           map[string]string `yaml:"api_keys" json:"api_keys"`
	RateLimitRPS      float64           `yaml:"rate_limit_rps" json:"rate_limit_rps"`
	LogLevel          string            `yaml:"log_level" json:"log_level"`
	ReadTimeout       time.Duration     `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout      time.Duration     `yaml:"write_timeout" json:"write_timeout"`
	MaxRetries        int               `yaml:"max_retries" json:"max_retries"`
	RetryBaseDelay    time.Duration     `yaml:"retry_base_delay" json:"retry_base_delay"`
	TelemetryEnabled  bool              `yaml:"telemetry_enabled" json:"telemetry_enabled"`
	TelemetryPath     string            `yaml:"telemetry_path" json:"telemetry_path"`
	ScrapeInterval    time.Duration     `yaml:"scrape_interval" json:"scrape_interval"`
	CacheEnabled      bool              `yaml:"cache_enabled" json:"cache_enabled"`
	CacheTTL          time.Duration     `yaml:"cache_ttl" json:"cache_ttl"`
	CacheMaxEntries   int               `yaml:"cache_max_entries" json:"cache_max_entries"`
	GuardrailsEnabled bool              `yaml:"guardrails_enabled" json:"guardrails_enabled"`
	PIIBufferWords    int               `yaml:"pii_buffer_words" json:"pii_buffer_words"`
}

func Default() *Config {
	return &Config{
		ListenAddr:        ":8080",
		UpstreamBaseURL:   "https://api.z.ai/api/paas/v4",
		APIKeys:           make(map[string]string),
		RateLimitRPS:      10,
		LogLevel:          "info",
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		MaxRetries:        2,
		RetryBaseDelay:    500 * time.Millisecond,
		TelemetryEnabled:  true,
		TelemetryPath:     "/metrics",
		ScrapeInterval:    15 * time.Second,
		CacheEnabled:      false,
		CacheTTL:          1 * time.Hour,
		CacheMaxEntries:   10000,
		GuardrailsEnabled: false,
		PIIBufferWords:    5,
	}
}

func Load() (*Config, error) {
	cfg := Default()

	if path := os.Getenv("SENTINEL_CONFIG"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	if v := os.Getenv("SENTINEL_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("SENTINEL_UPSTREAM_BASE_URL"); v != "" {
		cfg.UpstreamBaseURL = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("SENTINEL_API_KEYS"); v != "" {
		if err := json.Unmarshal([]byte(v), &cfg.APIKeys); err != nil {
			return nil, fmt.Errorf("parsing SENTINEL_API_KEYS: %w", err)
		}
	}
	if v := os.Getenv("SENTINEL_RATE_LIMIT_RPS"); v != "" {
		if _, err := fmt.Sscanf(v, "%f", &cfg.RateLimitRPS); err != nil {
			return nil, fmt.Errorf("parsing SENTINEL_RATE_LIMIT_RPS: %w", err)
		}
	}
	if v := os.Getenv("SENTINEL_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("SENTINEL_UPSTREAM_API_KEY"); v != "" {
		cfg.UpstreamAPIKey = v
	}

	if len(cfg.APIKeys) == 0 {
		return nil, fmt.Errorf("at least one API key must be configured via SENTINEL_API_KEYS or config file")
	}
	if cfg.UpstreamBaseURL == "" {
		return nil, fmt.Errorf("upstream base URL is required")
	}

	return cfg, nil
}
