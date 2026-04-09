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
	upstreamURL string
	client      *http.Client
	maxRetries  int
	retryDelay  time.Duration
	logger      *slog.Logger
	tel         *telemetry.Manager
}

func NewHandler(upstreamURL string, maxRetries int, retryDelay time.Duration, logger *slog.Logger, tel *telemetry.Manager) *Handler {
	return &Handler{
		upstreamURL: upstreamURL,
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

	var streamTel *telemetry.StreamTelemetry
	if h.tel != nil {
		streamTel = telemetry.NewStreamTelemetry(req.Model, "openai")
		ctx := telemetry.ContextWithStreamTelemetry(r.Context(), streamTel)
		r = r.WithContext(ctx)
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.upstreamURL+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		h.logger.Error("failed to create upstream request", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	for key, values := range r.Header {
		if strings.EqualFold(key, "Host") {
			continue
		}
		for _, v := range values {
			upstreamReq.Header.Add(key, v)
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
			resp.Body.Close()
			h.logger.Info("upstream rate limited, retrying", "attempt", attempt, "model", req.Model)
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			h.logger.Warn("upstream server error", "status", resp.StatusCode, "attempt", attempt, "model", req.Model)
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
	if strings.Contains(contentType, "text/event-stream") {
		h.streamSSE(w, resp.Body, streamTel, req.Model)
	} else {
		io.Copy(w, resp.Body)
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

		if line == "" {
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
