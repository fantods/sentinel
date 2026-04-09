# Sentinel

Sentinel is a transparent proxy for OpenAI-compatible LLM APIs. It sits between clients and upstream providers, adding rate limiting, streaming PII redaction, and deep observability (TTFT, tokens/sec, token counts via Prometheus + Grafana).

## Quick Start

### 1. Start Sentinel

```bash
export OPENAI_API_KEY=sk-your-real-openai-key
docker compose up -d
```

Docker Compose reads your `OPENAI_API_KEY` from the environment and injects it into the Sentinel container. Gateway keys for client authentication are defined in `docker-compose.yml` (`sk-gateway-key-1` and `sk-gateway-key-2` by default).

### 2. Point your tool at Sentinel

Sentinel listens on `:8080` and accepts any OpenAI-compatible request under `/v1/`. Clients authenticate with a gateway key — Sentinel replaces it with your real OpenAI key before forwarding upstream.

#### Codex

```bash
OPENAI_BASE_URL=http://localhost:8080/v1 \
OPENAI_API_KEY=sk-gateway-key-1 \
codex
```

#### curl

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-gateway-key-1" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"Hello"}]}'
```

### 3. Observe

- **Health**: `GET http://localhost:8080/health`
- **Prometheus**: `http://localhost:9090/metrics`
- **Grafana**: `http://localhost:3000` (admin/admin)

## Configuration

Configuration is loaded from a JSON file (set via `SENTINEL_CONFIG`) and overridden by environment variables.

| Env Variable | Default | Description |
|---|---|---|
| `SENTINEL_UPSTREAM_BASE_URL` | `https://api.openai.com` | Upstream API base URL |
| `SENTINEL_UPSTREAM_API_KEY` | _(empty)_ | API key injected into upstream requests. If empty, the client's `Authorization` header is forwarded as-is |
| `SENTINEL_API_KEYS` | _(required)_ | JSON map of `gateway-key → tenant-id` for client authentication |
| `SENTINEL_LISTEN_ADDR` | `:8080` | Address to listen on |
| `SENTINEL_RATE_LIMIT_RPS` | `10` | Requests per second per tenant |
| `SENTINEL_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `SENTINEL_TELEMETRY_ENABLED` | `true` | Enable Prometheus metrics |
