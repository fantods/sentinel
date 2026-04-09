package telemetry

import (
	"testing"
	"time"
)

func TestStreamTelemetry_ProcessLine(t *testing.T) {
	st := NewStreamTelemetry("gpt-4o", "openai")

	st.ProcessLine(`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	if st.ttftRecorded {
		t.Error("empty content should not record TTFT")
	}

	st.ProcessLine(`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)
	if !st.ttftRecorded {
		t.Error("non-empty content should record TTFT")
	}
	if st.FirstTokenAt.IsZero() {
		t.Error("FirstTokenAt should be set")
	}
	if st.OutputTokens == 0 {
		t.Error("OutputTokens should be > 0 after content")
	}

	st.ProcessLine(`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" World!"},"finish_reason":null}]}`)
	if st.OutputTokens < 2 {
		t.Error("OutputTokens should accumulate")
	}

	st.ProcessLine(`data: [DONE]`)
	if st.OutputTokens < 2 {
		t.Error("DONE should not reset tokens")
	}
}

func TestStreamTelemetry_UsageExtraction(t *testing.T) {
	st := NewStreamTelemetry("gpt-4o", "openai")

	st.ProcessLine(`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"Hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	if st.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", st.InputTokens)
	}
	if st.OutputTokens != 5 {
		t.Errorf("expected 5 output tokens (from usage), got %d", st.OutputTokens)
	}
}

func TestStreamTelemetry_TTFT(t *testing.T) {
	st := NewStreamTelemetry("gpt-4o", "openai")
	if st.TTFT() != 0 {
		t.Error("TTFT should be 0 before first token")
	}

	st.ProcessLine(`data: {"choices":[{"delta":{"content":"Hi"}}]}`)
	ttft := st.TTFT()
	if ttft <= 0 {
		t.Error("TTFT should be positive after first token")
	}
	if ttft > 1 {
		t.Error("TTFT should be sub-second in tests")
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"a", 1},
		{"ab", 1},
		{"abcd", 1},
		{"abcde", 2},
		{"Hello World", 3},
	}
	for _, tt := range tests {
		got := estimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("estimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestStreamTelemetry_IgnoresNonDataLines(t *testing.T) {
	st := NewStreamTelemetry("gpt-4o", "openai")
	st.ProcessLine(`: this is a comment`)
	st.ProcessLine(``)
	st.ProcessLine(`event: ping`)
	if st.chunkCount != 0 {
		t.Errorf("expected 0 chunks, got %d", st.chunkCount)
	}
}

func TestStreamTelemetry_Duration(t *testing.T) {
	st := NewStreamTelemetry("gpt-4o", "openai")
	time.Sleep(10 * time.Millisecond)
	d := st.Duration()
	if d < 0.01 {
		t.Errorf("duration should be at least 10ms, got %f", d)
	}
}
