package pricing

type ModelPrice struct {
	Input  float64
	Output float64
}

var table = map[string]ModelPrice{
	"gpt-4o":            {Input: 2.50 / 1_000_000, Output: 10.00 / 1_000_000},
	"gpt-4o-2024-08-06": {Input: 2.50 / 1_000_000, Output: 10.00 / 1_000_000},
	"gpt-4o-mini":       {Input: 0.15 / 1_000_000, Output: 0.60 / 1_000_000},
	"gpt-4-turbo":       {Input: 10.00 / 1_000_000, Output: 30.00 / 1_000_000},
	"gpt-4":             {Input: 30.00 / 1_000_000, Output: 60.00 / 1_000_000},
	"gpt-3.5-turbo":     {Input: 0.50 / 1_000_000, Output: 1.50 / 1_000_000},
	"o1":                {Input: 15.00 / 1_000_000, Output: 60.00 / 1_000_000},
	"o1-mini":           {Input: 3.00 / 1_000_000, Output: 12.00 / 1_000_000},
	"o1-pro":            {Input: 150.00 / 1_000_000, Output: 600.00 / 1_000_000},
	"o3-mini":           {Input: 1.10 / 1_000_000, Output: 4.40 / 1_000_000},
}

func CalculateCost(model string, inputTokens, outputTokens int) float64 {
	price, ok := table[model]
	if !ok {
		return 0
	}
	return float64(inputTokens)*price.Input + float64(outputTokens)*price.Output
}

func Lookup(model string) (ModelPrice, bool) {
	p, ok := table[model]
	return p, ok
}
