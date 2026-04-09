package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mattemmons/sentinel/internal/middleware"
	"github.com/mattemmons/sentinel/internal/telemetry"
)

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type Handler struct {
	upstreamURL    string
	upstreamAPIKey string
	client         *http.Client
	maxRetries     int
	retryDelay     time.Duration
	logger         *slog.Logger
	tel            *telemetry.Manager
}

func NewHandler(upstreamURL, upstreamAPIKey string, maxRetries int, retryDelay time.Duration, logger *slog.Logger, tel *telemetry.Manager) *Handler {
	return &Handler{
		upstreamURL:    upstreamURL,
		upstreamAPIKey: upstreamAPIKey,
		client: &http.Client{
			Timeout: 0,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		maxRetries: maxRetries,
		retryDelay: retryDelay,
		logger:     logger,
		tel:        tel,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("failed to read request body", "error", err)
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.logger.Error("failed to parse request body", "error", err)
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Could not parse request body")
		return
	}

	h.logger.Info("proxying request", "model", req.Model, "stream", req.Stream, "path", r.URL.Path)

	provider := extractProvider(h.upstreamURL)

	var streamTel *telemetry.StreamTelemetry
	if h.tel != nil {
		streamTel = telemetry.NewStreamTelemetry(req.Model, provider)
	}

	upstreamPath := stripVersionPrefix(r.URL.Path)

	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.upstreamURL+upstreamPath, bytes.NewReader(body))
	if err != nil {
		h.logger.Error("failed to create upstream request", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.logger.Info("forwarding to upstream", "url", h.upstreamURL+upstreamPath, "has_api_key", h.upstreamAPIKey != "")

	for key, values := range r.Header {
		if strings.EqualFold(key, "Host") || strings.EqualFold(key, "Authorization") {
			continue
		}
		for _, v := range values {
			upstreamReq.Header.Add(key, v)
		}
	}

	if h.upstreamAPIKey != "" {
		upstreamReq.Header.Set("Authorization", "Bearer "+h.upstreamAPIKey)
	} else {
		for _, v := range r.Header.Values("Authorization") {
			upstreamReq.Header.Add("Authorization", v)
		}
	}

	var resp *http.Response
	var lastErr error

	for attempt := 0; attempt <= h.maxRetries; attempt++ {
		if attempt > 0 {
			delay := h.retryDelay * time.Duration(1<<(attempt-1))
			h.logger.Info("retrying upstream request", "attempt", attempt, "delay", delay, "model", req.Model)
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				http.Error(w, "request cancelled", http.StatusRequestTimeout)
				return
			}
			upstreamReq.Body = io.NopCloser(bytes.NewReader(body))
		}

		resp, lastErr = h.client.Do(upstreamReq)
		if lastErr != nil {
			h.logger.Warn("upstream request failed", "error", lastErr, "attempt", attempt, "model", req.Model)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if isNonRetryableError(errBody) {
				h.logger.Warn("upstream non-retryable error", "model", req.Model, "body", string(errBody))
				lastErr = fmt.Errorf("upstream non-retryable error: %s", string(errBody))
				break
			}
			h.logger.Warn("upstream rate limited", "attempt", attempt, "model", req.Model, "body", string(errBody))
			if attempt == h.maxRetries {
				lastErr = fmt.Errorf("upstream rate limited after %d retries", h.maxRetries)
				break
			}
			continue
		}

		if resp.StatusCode >= 500 {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			h.logger.Warn("upstream server error", "status", resp.StatusCode, "attempt", attempt, "model", req.Model, "body", string(errBody))
			if attempt == h.maxRetries {
				lastErr = fmt.Errorf("upstream server error %d after %d retries", resp.StatusCode, h.maxRetries)
				break
			}
			continue
		}

		break
	}

	if lastErr != nil || resp == nil {
		h.logger.Error("all retries exhausted", "error", lastErr, "model", req.Model)
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "All retry attempts to upstream provider failed")
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	contentType := resp.Header.Get("Content-Type")
	h.logger.Debug("upstream response", "status", resp.StatusCode, "content_type", contentType, "model", req.Model)

	isSSE := strings.Contains(contentType, "text/event-stream") || (req.Stream && resp.StatusCode == 200)
	if isSSE {
		h.streamSSE(w, resp.Body, streamTel, req.Model)
	} else {
		respBody, _ := io.ReadAll(resp.Body)
		w.Write(respBody)

		if streamTel != nil {
			var chatResp struct {
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(respBody, &chatResp) == nil && chatResp.Usage != nil {
				streamTel.InputTokens = chatResp.Usage.PromptTokens
				streamTel.OutputTokens = chatResp.Usage.CompletionTokens
			}
		}
	}

	if streamTel != nil && h.tel != nil {
		streamTel.Tenant = middleware.TenantFromContext(r.Context())
		telemetry.RecordStream(h.tel.Instruments(), streamTel, resp.StatusCode)
		h.logger.Info("request completed",
			"model", req.Model,
			"stream", req.Stream,
			"status", resp.StatusCode,
			"ttft_ms", streamTel.TTFT()*1000,
			"output_tokens", streamTel.OutputTokens,
			"input_tokens", streamTel.InputTokens,
			"tps", streamTel.TPS(),
			"duration_ms", streamTel.Duration()*1000,
		)
	} else if resp.StatusCode >= 400 && h.tel != nil {
		st := telemetry.NewStreamTelemetry(req.Model, provider)
		st.Tenant = middleware.TenantFromContext(r.Context())
		telemetry.RecordStream(h.tel.Instruments(), st, resp.StatusCode)
	}
}

func (h *Handler) streamSSE(w http.ResponseWriter, body io.Reader, streamTel *telemetry.StreamTelemetry, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.logger.Error("response writer does not support flushing")
		io.Copy(w, body)
		return
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if streamTel != nil {
			streamTel.ProcessLine(line)
		}

		fmt.Fprintf(w, "%s\n", line)

		if line == "" || strings.HasPrefix(line, "data:") {
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		h.logger.Error("SSE stream read error", "error", err)
	}

	if streamTel != nil {
		h.logger.Info("stream completed",
			"model", model,
			"ttft_ms", streamTel.TTFT()*1000,
			"output_tokens", streamTel.OutputTokens,
			"tps", streamTel.TPS(),
			"duration_ms", streamTel.Duration()*1000,
		)
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    errType,
			"code":    status,
		},
	})
}

func extractProvider(upstreamURL string) string {
	if strings.Contains(upstreamURL, "bigmodel") || strings.Contains(upstreamURL, "z.ai") {
		return "zhipu"
	}
	if strings.Contains(upstreamURL, "anthropic") {
		return "anthropic"
	}
	return "openai"
}

func stripVersionPrefix(path string) string {
	if len(path) >= 4 && path[0] == '/' && path[1] == 'v' && path[3] == '/' {
		return path[3:]
	}
	return path
}

var nonRetryableCodes = []string{
	"insufficient_quota",
	"invalid_api_key",
	"model_not_found",
	"context_length_exceeded",
	"content_policy_violation",
	"1113",
}

func isNonRetryableError(body []byte) bool {
	var errResp struct {
		Error struct {
			Code interface{} `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errResp) != nil {
		return false
	}
	code := fmt.Sprintf("%v", errResp.Error.Code)
	for _, c := range nonRetryableCodes {
		if code == c {
			return true
		}
	}
	return false
}
