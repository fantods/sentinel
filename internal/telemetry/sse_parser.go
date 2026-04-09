package telemetry

import (
	"encoding/json"
	"strings"
	"time"
)

type SSEChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *SSEUsage `json:"usage,omitempty"`
}

type SSEUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type StreamTelemetry struct {
	StartTime    time.Time
	FirstTokenAt time.Time
	InputTokens  int
	OutputTokens int
	Model        string
	Tenant       string
	Provider     string
	Status       string
	ttftRecorded bool
	chunkCount   int
}

func NewStreamTelemetry(model, provider string) *StreamTelemetry {
	return &StreamTelemetry{
		StartTime: time.Now(),
		Model:     model,
		Provider:  provider,
		Status:    "success",
	}
}

func (st *StreamTelemetry) ProcessLine(line string) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}

	payload := strings.TrimPrefix(line, "data: ")
	if payload == "[DONE]" {
		return
	}

	var chunk SSEChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return
	}

	st.chunkCount++

	if !st.ttftRecorded {
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				st.FirstTokenAt = time.Now()
				st.ttftRecorded = true
				break
			}
		}
	}

	for _, choice := range chunk.Choices {
		st.OutputTokens += estimateTokens(choice.Delta.Content)
	}

	if chunk.Usage != nil {
		st.InputTokens = chunk.Usage.PromptTokens
		if chunk.Usage.CompletionTokens > 0 {
			st.OutputTokens = chunk.Usage.CompletionTokens
		}
	}
}

func (st *StreamTelemetry) TTFT() float64 {
	if st.FirstTokenAt.IsZero() {
		return 0
	}
	return st.FirstTokenAt.Sub(st.StartTime).Seconds()
}

func (st *StreamTelemetry) TPS() float64 {
	if st.FirstTokenAt.IsZero() || st.OutputTokens == 0 {
		return 0
	}
	duration := time.Since(st.FirstTokenAt).Seconds()
	if duration <= 0 {
		return 0
	}
	return float64(st.OutputTokens) / duration
}

func (st *StreamTelemetry) Duration() float64 {
	return time.Since(st.StartTime).Seconds()
}

func estimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text) + 3) / 4
}
