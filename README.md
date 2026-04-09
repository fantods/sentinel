# Sentinel

Sentinel is a transparent proxy for OpenAI-compatible LLM APIs. It sits between clients and upstream providers, adding rate limiting, streaming PII redaction, and deep observability (TTFT, tokens/sec, token counts via Prometheus + Grafana).

Currently configured to proxy through [Z.AI (Zhipu)](https://docs.z.ai/api-reference/llm/chat-completion).

## Quick Start

### 1. Start Sentinel

```bash
export ZAI_API_KEY=your-zhipu-api-key
docker compose up -d
```

Docker Compose reads your `ZAI_API_KEY` from the environment and injects it into the Sentinel container. Gateway keys for client authentication are defined in `docker-compose.yml` (`sk-gateway-key-1` and `sk-gateway-key-2` by default).

### 2. Point your tool at Sentinel

Sentinel listens on `:8080` and accepts OpenAI-compatible requests under `/v1/`. It strips the `/v1` prefix and forwards to the upstream Z.AI API. Clients authenticate with a gateway key — Sentinel replaces it with your real Z.AI key before forwarding upstream.

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
  -d '{"model":"glm-4.6","stream":true,"messages":[{"role":"user","content":"Hello"}]}'
```

### 3. Observe

- **Health**: `GET http://localhost:8080/health`
- **Prometheus**: `http://localhost:9090/metrics`
- **Grafana**: `http://localhost:3000` (admin/admin)

## Switching Providers

To use OpenAI instead of Z.AI, set the environment variable:

```bash
export OPENAI_API_KEY=sk-your-openai-key
SENTINEL_UPSTREAM_BASE_URL=https://api.openai.com/v1 \
SENTINEL_UPSTREAM_API_KEY=$OPENAI_API_KEY \
docker compose up -d
```

## Configuration

Configuration is loaded from a JSON file (set via `SENTINEL_CONFIG`) and overridden by environment variables.

| Env Variable | Default | Description |
|---|---|---|
| `SENTINEL_UPSTREAM_BASE_URL` | `https://api.z.ai/api/paas/v4` | Upstream API base URL (includes version path) |
| `SENTINEL_UPSTREAM_API_KEY` | _(empty)_ | API key injected into upstream requests. If empty, the client's `Authorization` header is forwarded as-is |
| `SENTINEL_API_KEYS` | _(required)_ | JSON map of `gateway-key → tenant-id` for client authentication |
| `SENTINEL_LISTEN_ADDR` | `:8080` | Address to listen on |
| `SENTINEL_RATE_LIMIT_RPS` | `10` | Requests per second per tenant |
| `SENTINEL_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `SENTINEL_TELEMETRY_ENABLED` | `true` | Enable Prometheus metrics |
