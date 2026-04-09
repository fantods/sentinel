# Sentinel — LLM Gateway Implementation Plan

## Overview

Sentinel is a transparent MitM proxy for OpenAI-compatible LLM APIs. It sits between clients and upstream providers, adding semantic caching, streaming PII redaction, and deep observability. This plan covers all phases with detailed implementation guidance for Phase 3 (OpenTelemetry + Grafana).

---

## Project Structure

```
sentinel/
├── cmd/
│   └── sentinel/
│       └── main.go                 # Entry point, wires dependencies
├── internal/
│   ├── config/
│   │   └── config.go               # Env/YAML config loading
│   ├── server/
│   │   └── server.go               # HTTP server setup, middleware chain
│   ├── proxy/
│   │   ├── handler.go              # /v1/chat/completions handler
│   │   ├── sse.go                  # SSE stream reader/writer
│   │   └── upstream.go             # Upstream HTTP client, retry logic
│   ├── auth/
│   │   └── keys.go                 # API key validation, key-to-tenant mapping
│   ├── middleware/
│   │   ├── ratelimit.go            # Per-key rate limiting
│   │   ├── auth.go                 # Authentication middleware
│   │   ├── telemetry.go            # Request-scoped telemetry middleware
│   │   └── recovery.go             # Panic recovery
│   ├── cache/
│   │   ├── cache.go                # Cache interface + orchestrator
│   │   ├── exact.go                # Tier 1: SHA-256 exact match
│   │   └── semantic.go             # Tier 2: Embedding-based semantic match
│   ├── guardrails/
│   │   ├── pii.go                  # PII detection + regex patterns
│   │   ├── buffer.go               # Sliding buffer for streaming redaction
│   │   └── injection.go            # Prompt injection detection
│   ├── telemetry/
│   │   ├── otel.go                 # OTel SDK init, Prometheus exporter
│   │   ├── instruments.go          # All metric instrument definitions
│   │   ├── sse_parser.go           # SSE chunk parser (token counting)
│   │   ├── cost.go                 # Model pricing table + cost calculation
│   │   └── span_helper.go          # Span creation helpers
│   └── pricing/
│       └── table.go                # Per-model input/output pricing data
├── grafana/
│   ├── provisioning/
│   │   ├── dashboards/
│   │   │   ├── dashboard.yaml      # Dashboard provider config
│   │   │   └── sentinel.json       # Pre-built LLM dashboard
│   │   └── datasources/
│   │       └── prometheus.yaml     # Auto-provisioned Prometheus DS
│   └── dashboards/
│       └── sentinel-overview.json  # The actual dashboard JSON model
├── prometheus/
│   └── prometheus.yml              # Scrape config
├── docker-compose.yml              # Gateway + Prometheus + Grafana
├── Dockerfile                      # Multi-stage Go build
├── Makefile
├── go.mod
├── go.sum
└── config.example.yaml
```

---

## Phase 1 — Dumb Proxy

**Goal:** Transparent SSE passthrough. Zero transformation. Client cannot tell the gateway is there.

### 1.1 Go Module & Entry Point

- `go mod init github.com/mattemmons/sentinel`
- `cmd/sentinel/main.go`: parse config, initialize logger, start HTTP server, handle graceful shutdown via `signal.NotifyContext`.

### 1.2 HTTP Reverse Proxy

`internal/proxy/handler.go`:
- Accept `POST /v1/chat/completions`.
- Create an `http.Request` cloning the original headers, body, and URL path to the upstream base URL (e.g., `https://api.openai.com/v1/chat/completions`).
- Use `httputil.ReverseProxy` OR a manual `http.Client` with a custom `Transport`.
- **Critical:** Set `FlushInterval: -1` on the response writer so SSE chunks flush immediately. Without this, Go's `ReverseProxy` buffers and clients see delayed streaming.
- Copy all response headers (`Content-Type: text/event-stream`, `X-*`, etc.) verbatim.

### 1.3 SSE Stream Handling

`internal/proxy/sse.go`:
- Wrap the upstream response body in a `bufio.Scanner`.
- Read line-by-line. SSE format is:
  ```
  data: {"id":"...","choices":[{"delta":{"content":"Hello"}}]}\n
  \n
  ```
- The last chunk is always `data: [DONE]\n\n`.
- Write each line to the downstream `http.ResponseWriter` and call `Flush()` after each event.
- **Telemetry hook point (for Phase 3):** For each non-`[DONE]` chunk, parse the JSON to extract `choices[0].delta.content` and any `usage` field.

### 1.4 API Key Auth

`internal/auth/keys.go`:
- Gateway API keys are stored as a map or loaded from a file/env.
- Incoming request must have `Authorization: Bearer <key>`.
- Map each key to a tenant ID for cost attribution.
- Return `401` with OpenAI-compatible error JSON on failure.

### 1.5 Configuration

`internal/config/config.go`:
- Load from `config.yaml` + env var overrides (`SENTINEL_*`).
- Fields: `listen_addr`, `upstream_base_url`, `api_keys` (map of key → tenant), `rate_limit_rps`, `log_level`.

### 1.6 Docker Setup

`Dockerfile`: Multi-stage build (Go 1.22 builder → scratch/distroless runtime).

`docker-compose.yml` (Phase 1: just the gateway):
```yaml
services:
  sentinel:
    build: .
    ports:
      - "8080:8080"
    environment:
      - SENTINEL_UPSTREAM_BASE_URL=https://api.openai.com
      - SENTINEL_API_KEYS={"sk-gateway-key-1":"tenant-a"}
```

### 1.7 Acceptance Criteria

- `curl` against gateway returns identical SSE stream as direct OpenAI call.
- Streaming begins within the same millisecond window as direct call.
- Non-streaming requests (`"stream": false`) also pass through correctly.
- `401` on missing/invalid key.

---

## Phase 2 — Core Proxy Hardening

**Goal:** Make the proxy production-ready with rate limiting, retries, and error handling.

### 2.1 Rate Limiting

`internal/middleware/ratelimit.go`:
- Token bucket per API key, using `golang.org/x/time/rate`.
- Return `429` with `Retry-After` header and OpenAI-compatible error JSON.
- Configurable `rate_limit_rps` per key.

### 2.2 Upstream Retry Logic

`internal/proxy/upstream.go`:
- Retry on `429` (rate limit from upstream) with exponential backoff.
- Retry on network timeouts (configurable max retries, default 2).
- **Do not retry** on `400` (bad request) or `401` (auth failure) — these are client errors.

### 2.3 Error Taxonomy

Map upstream error codes to gateway behavior:

| Upstream Error | HTTP Status | Gateway Action |
|---|---|---|
| `context_length_exceeded` | 400 | Return to client (their prompt is too long) |
| `rate_limit_exceeded` | 429 | Retry with backoff |
| `invalid_api_key` | 401 | Log alert, return 502 to client |
| `server_error` | 500/502/503 | Retry with backoff |
| `model_not_found` | 404 | Return to client |

### 2.4 Middleware Chain

Order matters:
1. Recovery (catch panics → 500)
2. Telemetry middleware (start timer, create request context)
3. Auth middleware
4. Rate limit middleware
5. Proxy handler

### 2.5 Acceptance Criteria

- Rate-limited keys get proper 429s.
- Upstream 500s trigger retries transparently.
- Client errors (400s) are not retried.

---

## Phase 3 — OpenTelemetry + Grafana Dashboard

**Goal:** LLM-specific observability with zero-impact on request latency. Pre-built Grafana dashboard deployed as code.

### Architecture Decision: Prometheus Exporter (Not OTLP Collector)

For a Docker Compose deployment, the simplest and most performant path is to run the Prometheus exporter directly in the gateway binary. No OTel Collector sidecar needed.

```
Client → Sentinel (Go binary)
              ├── handles request
              ├── records OTel metrics in-memory
              └── /metrics endpoint (Prometheus scrapes this)
                       
Prometheus ← scrapes /metrics from Sentinel
Grafana    ← queries Prometheus
```

### 3.1 Go Dependencies

Add to `go.mod`:
```
go.opentelemetry.io/otel
go.opentelemetry.io/otel/sdk
go.opentelemetry.io/otel/sdk/metric
go.opentelemetry.io/otel/exporters/prometheus
go.opentelemetry.io/otel/attribute
```

No OTLP exporter needed — Prometheus scrapes directly.

### 3.2 OTel SDK Initialization

`internal/telemetry/otel.go`:

```go
func InitTelemetry() (*sdkmetric.MeterProvider, error) {
    exporter, err := prometheus.New()  // OTel Prometheus exporter
    if err != nil { return nil, err }

    res := resource.NewWithAttributes(
        semconv.SchemaURL,
        semconv.ServiceName("sentinel"),
        semconv.ServiceVersion("0.1.0"),
    )

    mp := sdkmetric.NewMeterProvider(
        sdkmetric.WithResource(res),
        sdkmetric.WithReader(exporter),
    )
    otel.SetMeterProvider(mp)
    return mp, nil
}
```

- The Prometheus exporter registers an HTTP handler at `/metrics` on the gateway's own HTTP server.
- No separate OTel Collector process needed.

### 3.3 Metric Instrument Definitions

`internal/telemetry/instruments.go`:

Define all instruments once at startup. Store in a struct passed through the request context.

| Metric Name | Type | Unit | Labels | Description |
|---|---|---|---|---|
| `llm_request_duration_seconds` | Histogram | s | `model`, `provider`, `status` | Total request wall-clock time |
| `llm_ttft_seconds` | Histogram | s | `model`, `provider` | Time from request received to first token sent to client |
| `llm_tokens_per_second` | Histogram | tok/s | `model`, `provider` | Output token streaming speed |
| `llm_tokens_total` | Counter | {tokens} | `model`, `type` (input/output) | Total token consumption |
| `llm_cost_dollars` | Counter | USD | `model`, `tenant` | Running cost per model and tenant |
| `llm_requests_total` | Counter | {req} | `model`, `provider`, `status` | Request count |
| `llm_cache_result_total` | Counter | {result} | `model`, `tier` (exact/semantic), `result` (hit/miss) | Cache hit/miss rate |
| `llm_errors_total` | Counter | {err} | `model`, `error_type`, `provider` | Errors by taxonomy |
| `llm_active_requests` | UpDownCounter | {req} | `model` | Concurrent in-flight requests |

Histogram bucket boundaries for latency metrics:
```
[0.05, 0.1, 0.25, 0.5, 0.75, 1.0, 1.5, 2.5, 5.0, 10.0]
```

### 3.4 SSE Chunk Parser for Telemetry

`internal/telemetry/sse_parser.go`:

This is the core telemetry data extraction. For each SSE chunk:

1. Parse the JSON payload: `{"choices":[{"delta":{"content":"..."}}],"usage":{...}}`.
2. **Token counting (streaming):** Count the characters in each `delta.content`, estimate tokens using a simple heuristic (≈4 chars per token for English) OR wait for the final chunk's `usage` field for exact counts.
3. **TTFT detection:** The moment the first non-empty `delta.content` arrives, record `time.Now() - requestStartTime`.
4. **TPS calculation:** After the stream ends, compute `totalOutputTokens / (streamEndTime - ttftTime)`.
5. **Usage field:** OpenAI returns a `usage` object in the final chunk (when `stream_options: {"include_usage": true}` is set). Parse `prompt_tokens`, `completion_tokens`, `total_tokens` for exact counts.

```go
type SSETelemetryParser struct {
    startTime     time.Time
    firstTokenAt  time.Time
    outputTokens  int
    inputTokens   int
    model         string
    tenant        string
    instruments   *Instruments
    ttftRecorded  bool
}

func (p *SSETelemetryParser) ProcessChunk(line string) {
    if strings.HasPrefix(line, "data: [DONE]") {
        p.finalize()
        return
    }
    var chunk SSEChunk
    json.Unmarshal(payload, &chunk)
    
    // Track TTFT
    if !p.ttftRecorded && len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
        p.firstTokenAt = time.Now()
        p.ttftRecorded = true
        p.instruments.TTFT.Record(ctx, p.firstTokenAt.Sub(p.startTime).Seconds(), ...)
    }
    
    // Accumulate output tokens
    p.outputTokens += estimateTokens(chunk.Choices[0].Delta.Content)
    
    // Extract usage if present
    if chunk.Usage != nil {
        p.inputTokens = chunk.Usage.PromptTokens
    }
}
```

### 3.5 Cost Calculation Engine

`internal/telemetry/cost.go` + `internal/pricing/table.go`:

In-memory pricing table updated at build time (no database calls in request path):

```go
var modelPricing = map[string]ModelPrice{
    "gpt-4o":                  {Input: 2.50 / 1_000_000, Output: 10.00 / 1_000_000},
    "gpt-4o-mini":             {Input: 0.15 / 1_000_000, Output: 0.60 / 1_000_000},
    "gpt-4-turbo":             {Input: 10.00 / 1_000_000, Output: 30.00 / 1_000_000},
    "gpt-3.5-turbo":           {Input: 0.50 / 1_000_000, Output: 1.50 / 1_000_000},
    "o1":                      {Input: 15.00 / 1_000_000, Output: 60.00 / 1_000_000},
    "o1-mini":                 {Input: 3.00 / 1_000_000, Output: 12.00 / 1_000_000},
}

func CalculateCost(model string, inputTokens, outputTokens int) float64 {
    price := modelPricing[model]
    return float64(inputTokens)*price.Input + float64(outputTokens)*price.Output
}
```

- Cost is emitted as a metric increment, not written to any database.
- Enables Grafana queries like `increase(llm_cost_dollars[1h])` for hourly spend.

### 3.6 Telemetry Middleware

`internal/middleware/telemetry.go`:

Wraps each request with a span and context:

```go
func TelemetryMiddleware(instruments *telemetry.Instruments) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            
            // Parse model from request body (peek without consuming)
            model := peekModel(r.Body)
            
            // Wrap ResponseWriter to intercept first write (TTFT)
            tw := &telemetryWriter{ResponseWriter: w, start: start, ...}
            
            // Increment active requests
            instruments.ActiveRequests.Add(r.Context(), 1, ...)
            defer instruments.ActiveRequests.Add(r.Context(), -1, ...)
            
            next.ServeHTTP(tw, r)
            
            // Record request duration
            instruments.RequestDuration.Record(r.Context(), time.Since(start).Seconds(), ...)
        })
    }
}
```

### 3.7 Docker Compose — Full Observability Stack

`docker-compose.yml`:

```yaml
services:
  sentinel:
    build: .
    ports:
      - "8080:8080"
    environment:
      - SENTINEL_UPSTREAM_BASE_URL=https://api.openai.com
      - SENTINEL_API_KEYS={"sk-gateway-key-1":"tenant-a"}
    depends_on:
      - prometheus

  prometheus:
    image: prom/prometheus:v2.51.0
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus/prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus-data:/prometheus

  grafana:
    image: grafana/grafana:10.4.0
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_USER=admin
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_AUTH_ANONYMOUS_ENABLED=true
      - GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer
    volumes:
      - ./grafana/provisioning:/etc/grafana/provisioning
      - grafana-data:/var/lib/grafana
    depends_on:
      - prometheus

volumes:
  prometheus-data:
  grafana-data:
```

### 3.8 Prometheus Scrape Config

`prometheus/prometheus.yml`:

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: "sentinel"
    static_configs:
      - targets: ["sentinel:8080"]
    metrics_path: /metrics
    scrape_interval: 5s
```

5-second scrape interval for near-real-time LLM performance visibility.

### 3.9 Grafana Dashboard Provisioning

`grafana/provisioning/datasources/prometheus.yaml`:
```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: false
```

`grafana/provisioning/dashboards/dashboard.yaml`:
```yaml
apiVersion: 1
providers:
  - name: Sentinel
    orgId: 1
    folder: ""
    type: file
    disableDeletion: false
    updateIntervalSeconds: 30
    options:
      path: /etc/grafana/provisioning/dashboards
      foldersFromFilesStructure: false
```

### 3.10 Grafana Dashboard — Panel Specification

The dashboard JSON (`grafana/provisioning/dashboards/sentinel.json`) will contain these rows:

#### Row 1: Request Overview
| Panel | Type | Query / Description |
|---|---|---|
| Request Rate | Stat | `rate(llm_requests_total[5m])` by model |
| Error Rate % | Stat | `rate(llm_requests_total{status=~"5.."}[5m]) / rate(llm_requests_total[5m])` |
| Active Requests | Stat | `llm_active_requests` |
| Latency p50/p95/p99 | Graph | `histogram_quantile(0.5, rate(llm_request_duration_seconds_bucket[5m]))` |
| Request Volume | Time series | `sum by (model) (rate(llm_requests_total[5m]))` |

#### Row 2: LLM Performance (The Key Differentiator)
| Panel | Type | Query / Description |
|---|---|---|
| TTFT Distribution | Heatmap | `rate(llm_ttft_seconds_bucket[5m])` — shows provider latency |
| TTFT p50/p95/p99 | Stat | `histogram_quantile(...)` |
| Tokens/Second | Time series | `rate(llm_tokens_per_second_sum[5m]) / rate(llm_tokens_per_second_count[5m])` by model |
| TTFT by Model | Bar gauge | Comparison of average TTFT across models |

#### Row 3: Cost Analytics
| Panel | Type | Query / Description |
|---|---|---|
| Hourly Spend | Stat | `sum(increase(llm_cost_dollars[1h]))` |
| Cost by Model | Pie chart | `sum by (model) (increase(llm_cost_dollars[24h]))` |
| Cost by Tenant | Bar gauge | `sum by (tenant) (increase(llm_cost_dollars[24h]))` |
| Cost Over Time | Time series | `sum by (model) (rate(llm_cost_dollars[5m])) * 3600` |

#### Row 4: Token Analytics
| Panel | Type | Query / Description |
|---|---|---|
| Input vs Output Tokens | Stacked area | `sum by (type) (rate(llm_tokens_total[5m]))` |
| Tokens by Model | Bar chart | `sum by (model) (increase(llm_tokens_total[1h]))` |
| Token Rate | Stat | `sum(rate(llm_tokens_total{type="output"}[5m]))` tokens/sec |

#### Row 5: Cache Performance (Active after Phase 4)
| Panel | Type | Query / Description |
|---|---|---|
| Cache Hit Rate | Gauge | `rate(llm_cache_result_total{result="hit"}[5m]) / rate(llm_cache_result_total[5m])` |
| Hits vs Misses | Time series | `rate(llm_cache_result_total[5m])` by result and tier |

#### Row 6: Error Analysis
| Panel | Type | Query / Description |
|---|---|---|
| Error Rate by Type | Time series | `sum by (error_type) (rate(llm_errors_total[5m]))` |
| Errors by Provider | Table | Top errors with model, error_type, count |

#### Dashboard Variables
- `$model`: Label values from `llm_requests_total`
- `$tenant`: Label values from `llm_cost_dollars`
- `$provider`: Label values from `llm_requests_total`
- Time range: Last 1 hour (default), auto-refresh every 10s

### 3.11 Acceptance Criteria

- `docker compose up` starts gateway + Prometheus + Grafana with zero manual config.
- Grafana at `:3000` shows the Sentinel dashboard pre-loaded.
- TTFT, TPS, cost, and token metrics appear after sending test requests.
- P99 latency of gateway does not exceed 1ms overhead vs. direct calls.
- `/metrics` endpoint returns all defined OTel metrics in Prometheus format.

---

## Phase 4 — Semantic Caching

**Goal:** Two-tier cache that catches 30-40% of repeat traffic at sub-millisecond cost.

### 4.1 Tier 1: Exact Match (Hashing)

`internal/cache/exact.go`:
- Hash key: `SHA256(model + "\n" + system_prompt + "\n" + user_prompt + "\n" + temperature + "\n" + top_p)`.
- Store in a sync.Map or Redis (if deployed). For single-instance, in-memory with TTL (default 1 hour).
- Only cache when `temperature == 0` (non-deterministic outputs should not be cached).
- Return immediately on hit, replaying the SSE chunks from the cached response.

### 4.2 Tier 2: Semantic Match (Embeddings)

`internal/cache/semantic.go`:
- Only reached on Tier 1 miss.
- Generate embedding for `user_prompt` using an external embedding service (small Python FastAPI microservice or ONNX runtime in Go).
- Search for cosine similarity > 0.98 against cached embeddings in Redis (RediSearch Vector) or Qdrant.
- Cache key: `Hash(system_prompt) + embedding + temperature + top_p`.

### 4.3 Cache Key Design

```
if temperature > 0 {
    return nil  // Don't cache non-deterministic responses
}
key = sha256(model || system_prompt || user_prompt || temperature || top_p)
```

### 4.4 Cached Response Replay

- Cache the full SSE stream (list of chunk strings), not just the final text.
- Replay chunks with appropriate `data:` framing and timing (optional: add configurable artificial delay).
- This preserves format compatibility for clients expecting SSE.

### 4.5 Acceptance Criteria

- Identical requests return from cache in <1ms.
- Semantic matches (>0.98 similarity) return from cache in <50ms.
- Cache hit rate is measurable in Grafana (Phase 3 dashboards).
- Non-zero-temperature requests are never cached.

---

## Phase 5 — Guardrails (Streaming PII Redaction)

**Goal:** Real-time PII redaction across SSE chunk boundaries without breaking streaming.

### 5.1 Sliding Buffer

`internal/guardrails/buffer.go`:
- Hold back the last 3-5 words (configurable) from the stream.
- As new chunks arrive, push oldest words to client while holding newest.
- Pattern matching (regex + NLP) runs only on the buffer content.

### 5.2 PII Detection

`internal/guardrails/pii.go`:
- Regex patterns for: SSN, phone numbers, email addresses, credit card numbers, IP addresses.
- Named entity recognition for: person names, locations (using lightweight library or external service).
- Replace matches with `[REDACTED]` before flushing to client.

### 5.3 Prompt Injection Detection

`internal/guardrails/injection.go`:
- Pre-processing only (before forwarding to upstream).
- Pattern-based detection: "ignore previous instructions", "system prompt", etc.
- Configurable action: block (return 400), warn (add header), or pass through with logging.

### 5.4 Backpressure Management

- If PII scanning is slower than token generation, apply TCP backpressure on the upstream connection.
- Implement using `io.Copy` with a bounded buffer; when buffer is full, upstream reads block naturally.
- Configurable buffer size (default: 4KB).

### 5.5 Acceptance Criteria

- PII split across chunks is detected and redacted.
- Streaming latency overhead from buffer is <5ms per chunk.
- Memory usage is bounded regardless of response length.
- No PII leaks in end-to-end tests with known test patterns.

---

## Cross-Cutting Concerns

### Logging
- Structured JSON logging via `log/slog` (stdlib, no dependency).
- Request ID in every log line (from middleware).
- Log level configurable via `config.yaml`.

### Graceful Shutdown
- On SIGINT/SIGTERM: stop accepting new requests, drain in-flight requests (30s timeout), flush telemetry, exit.
- Use `http.Server.Shutdown()`.

### Testing Strategy
- Unit tests for SSE parser, cost calculator, cache key generation, PII regex.
- Integration test: run gateway in Docker, send real SSE requests, verify streaming output matches upstream.
- Telemetry test: send requests, scrape `/metrics`, verify counters/histograms.

---

## Implementation Order

| Step | Phase | Effort | Key Deliverable |
|---|---|---|---|
| 1 | Phase 1 | 2-3 days | Dumb proxy with SSE passthrough |
| 2 | Phase 2 | 1-2 days | Rate limiting, retries, error taxonomy |
| 3 | Phase 3 | 3-4 days | OTel metrics, Prometheus, Grafana dashboard |
| 4 | Phase 4 | 3-5 days | Exact + semantic caching |
| 5 | Phase 5 | 3-4 days | Streaming PII redaction buffer |

**Total estimated effort: 12-18 days**
