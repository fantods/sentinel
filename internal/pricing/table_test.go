package pricing

import (
	"math"
	"testing"
)

func TestCalculateCost(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		inputTokens  int
		outputTokens int
		wantGT       float64
	}{
		{
			name:  "gpt-4o 1M input tokens",
			model: "gpt-4o", inputTokens: 1_000_000, outputTokens: 0,
			wantGT: 2.40,
		},
		{
			name:  "gpt-4o 1M output tokens",
			model: "gpt-4o", inputTokens: 0, outputTokens: 1_000_000,
			wantGT: 9.90,
		},
		{
			name:  "gpt-4o-mini small request",
			model: "gpt-4o-mini", inputTokens: 1000, outputTokens: 500,
			wantGT: 0,
		},
		{
			name:  "unknown model",
			model: "nonexistent-model", inputTokens: 1000, outputTokens: 500,
			wantGT: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateCost(tt.model, tt.inputTokens, tt.outputTokens)
			if tt.wantGT > 0 && got <= tt.wantGT {
				t.Errorf("CalculateCost(%s, %d, %d) = %f, want > %f", tt.model, tt.inputTokens, tt.outputTokens, got, tt.wantGT)
			}
			if tt.model == "nonexistent-model" && got != 0 {
				t.Errorf("unknown model should cost 0, got %f", got)
			}
		})
	}
}

func TestCalculateCost_GPT4oExact(t *testing.T) {
	price, ok := Lookup("gpt-4o")
	if !ok {
		t.Fatal("gpt-4o should exist in pricing table")
	}

	inputTokens := 500_000
	outputTokens := 100_000
	expected := float64(inputTokens)*price.Input + float64(outputTokens)*price.Output
	got := CalculateCost("gpt-4o", inputTokens, outputTokens)

	if math.Abs(got-expected) > 0.000001 {
		t.Errorf("got %f, expected %f", got, expected)
	}
}
