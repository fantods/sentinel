package main

import (
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

type fakeMetrics struct {
	mu sync.Mutex

	requestTotal    float64
	requestDuration *histogram
	ttft            *histogram
	tps             *histogram
	tokensInput     float64
	tokensOutput    float64
	costDollars     float64
	cacheHits       float64
	cacheMisses     float64
	errorsRateLimit float64
	errorsTimeout   float64
	errorsAuth      float64
	activeRequests  float64
}

type histogram struct {
	buckets []float64
	counts  []float64
	sum     float64
	count   float64
}

func newHistogram(bounds ...float64) *histogram {
	return &histogram{buckets: bounds, counts: make([]float64, len(bounds)+1)}
}

func (h *histogram) observe(v float64) {
	h.sum += v
	h.count++
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
	h.counts[len(h.buckets)]++
}

func main() {
	m := &fakeMetrics{
		requestDuration: newHistogram(0.1, 0.25, 0.5, 1, 2.5, 5, 10),
		ttft:            newHistogram(0.05, 0.1, 0.2, 0.5, 1, 2, 5),
		tps:             newHistogram(10, 20, 30, 50, 80, 120, 200),
	}

	go m.tick()

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, m.render())
	})

	fmt.Println("fake metrics generator on :9999/metrics")
	http.ListenAndServe(":9999", nil)
}

func (m *fakeMetrics) tick() {
	models := []string{"glm-4.7-flash", "glm-5.1", "glm-4.6"}

	for range time.Tick(2 * time.Second) {
		m.mu.Lock()

		for _, model := range models {
			rate := 2 + rand.Float64()*4
			if model == "glm-4.7-flash" {
				rate *= 2.5
			}
			n := math.Ceil(rate * 2)

			for i := 0; i < int(n); i++ {
				m.requestTotal++

				duration := 0.3 + rand.Float64()*2.5
				if model == "glm-4.7-flash" {
					duration *= 0.5
				}
				m.requestDuration.observe(duration)

				ttft := 0.1 + rand.Float64()*0.8
				if model == "glm-5.1" {
					ttft *= 1.5
				}
				m.ttft.observe(ttft)

				tps := 30 + rand.Float64()*70
				if model == "glm-4.7-flash" {
					tps *= 1.5
				}
				m.tps.observe(tps)

				inputTokens := float64(50 + rand.Intn(200))
				outputTokens := float64(100 + rand.Intn(400))
				m.tokensInput += inputTokens
				m.tokensOutput += outputTokens

				cost := inputTokens*0.0000006 + outputTokens*0.0000022
				if model == "glm-4.7-flash" {
					cost = 0
				}
				m.costDollars += cost

				if rand.Float64() < 0.4 {
					m.cacheHits++
				} else {
					m.cacheMisses++
				}

				r := rand.Float64()
				if r < 0.015 {
					m.errorsRateLimit++
				} else if r < 0.025 {
					m.errorsTimeout++
				} else if r < 0.03 {
					m.errorsAuth++
				}
			}
		}

		m.activeRequests = float64(1 + rand.Intn(8))

		m.mu.Unlock()
	}
}

func (m *fakeMetrics) render() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out string

	out += "# HELP llm_requests_total Total request count\n"
	out += "# TYPE llm_requests_total counter\n"
	out += fmt.Sprintf("llm_requests_total{model=\"glm-4.7-flash\",provider=\"zhipu\",status=\"success\"} %.0f\n", m.requestTotal*0.55)
	out += fmt.Sprintf("llm_requests_total{model=\"glm-5.1\",provider=\"zhipu\",status=\"success\"} %.0f\n", m.requestTotal*0.28)
	out += fmt.Sprintf("llm_requests_total{model=\"glm-4.6\",provider=\"zhipu\",status=\"success\"} %.0f\n", m.requestTotal*0.14)
	out += fmt.Sprintf("llm_requests_total{model=\"glm-4.7-flash\",provider=\"zhipu\",status=\"error\"} %.0f\n", m.requestTotal*0.015)
	out += fmt.Sprintf("llm_requests_total{model=\"glm-5.1\",provider=\"zhipu\",status=\"error\"} %.0f\n", m.requestTotal*0.01)
	out += fmt.Sprintf("llm_requests_total{model=\"glm-4.6\",provider=\"zhipu\",status=\"error\"} %.0f\n", m.requestTotal*0.005)
	out += "\n"

	writeHistogram := func(name string, h *histogram, labels string) {
		for i, b := range h.buckets {
			out += fmt.Sprintf("%s_bucket{le=\"%.2f\",%s} %.0f\n", name, b, labels, h.counts[i])
		}
		out += fmt.Sprintf("%s_bucket{le=\"+Inf\",%s} %.0f\n", name, labels, h.counts[len(h.buckets)])
		out += fmt.Sprintf("%s_sum{%s} %.2f\n", name, labels, h.sum)
		out += fmt.Sprintf("%s_count{%s} %.0f\n", name, labels, h.count)
	}

	out += "# HELP llm_request_duration_seconds Total request wall-clock time\n"
	out += "# TYPE llm_request_duration_seconds histogram\n"
	for _, model := range []string{"glm-4.7-flash", "glm-5.1", "glm-4.6"} {
		scale := map[string]float64{"glm-4.7-flash": 0.5, "glm-5.1": 1.3, "glm-4.6": 1.0}[model]
		h := newHistogram(0.1, 0.25, 0.5, 1, 2.5, 5, 10)
		n := m.requestTotal * scale / 2.5
		for i := 0; i < int(n*0.3); i++ {
			h.observe(0.2*scale + rand.Float64()*1.5*scale)
		}
		writeHistogram("llm_request_duration_seconds", h, fmt.Sprintf("model=\"%s\",provider=\"zhipu\"", model))
	}
	out += "\n"

	out += "# HELP llm_ttft_seconds Time to first token\n"
	out += "# TYPE llm_ttft_seconds histogram\n"
	for _, model := range []string{"glm-4.7-flash", "glm-5.1", "glm-4.6"} {
		scale := map[string]float64{"glm-4.7-flash": 0.6, "glm-5.1": 1.4, "glm-4.6": 1.0}[model]
		h := newHistogram(0.05, 0.1, 0.2, 0.5, 1, 2, 5)
		n := m.requestTotal * scale / 2.5
		for i := 0; i < int(n*0.3); i++ {
			h.observe(0.08*scale + rand.Float64()*0.6*scale)
		}
		writeHistogram("llm_ttft_seconds", h, fmt.Sprintf("model=\"%s\",provider=\"zhipu\"", model))
	}
	out += "\n"

	out += "# HELP llm_tokens_per_second Output token streaming speed\n"
	out += "# TYPE llm_tokens_per_second histogram\n"
	for _, model := range []string{"glm-4.7-flash", "glm-5.1", "glm-4.6"} {
		scale := map[string]float64{"glm-4.7-flash": 1.8, "glm-5.1": 0.8, "glm-4.6": 1.0}[model]
		h := newHistogram(10, 20, 30, 50, 80, 120, 200)
		n := m.requestTotal * scale / 3.0
		for i := 0; i < int(n*0.2); i++ {
			h.observe(30*scale + rand.Float64()*60*scale)
		}
		writeHistogram("llm_tokens_per_second", h, fmt.Sprintf("model=\"%s\",provider=\"zhipu\"", model))
	}
	out += "\n"

	out += "# HELP llm_tokens_total Total token consumption\n"
	out += "# TYPE llm_tokens_total counter\n"
	out += fmt.Sprintf("llm_tokens_total{model=\"glm-4.7-flash\",type=\"input\"} %.0f\n", m.tokensInput*0.5)
	out += fmt.Sprintf("llm_tokens_total{model=\"glm-4.7-flash\",type=\"output\"} %.0f\n", m.tokensOutput*0.5)
	out += fmt.Sprintf("llm_tokens_total{model=\"glm-5.1\",type=\"input\"} %.0f\n", m.tokensInput*0.3)
	out += fmt.Sprintf("llm_tokens_total{model=\"glm-5.1\",type=\"output\"} %.0f\n", m.tokensOutput*0.3)
	out += fmt.Sprintf("llm_tokens_total{model=\"glm-4.6\",type=\"input\"} %.0f\n", m.tokensInput*0.2)
	out += fmt.Sprintf("llm_tokens_total{model=\"glm-4.6\",type=\"output\"} %.0f\n", m.tokensOutput*0.2)
	out += "\n"

	out += "# HELP llm_cost_dollars Running cost attribution\n"
	out += "# TYPE llm_cost_dollars counter\n"
	out += fmt.Sprintf("llm_cost_dollars{model=\"glm-5.1\",tenant=\"tenant-a\"} %.4f\n", m.costDollars*0.6)
	out += fmt.Sprintf("llm_cost_dollars{model=\"glm-5.1\",tenant=\"tenant-b\"} %.4f\n", m.costDollars*0.15)
	out += fmt.Sprintf("llm_cost_dollars{model=\"glm-4.6\",tenant=\"tenant-a\"} %.4f\n", m.costDollars*0.15)
	out += fmt.Sprintf("llm_cost_dollars{model=\"glm-4.6\",tenant=\"tenant-b\"} %.4f\n", m.costDollars*0.1)
	out += "llm_cost_dollars{model=\"glm-4.7-flash\",tenant=\"tenant-a\"} 0\n"
	out += "llm_cost_dollars{model=\"glm-4.7-flash\",tenant=\"tenant-b\"} 0\n"
	out += "\n"

	out += "# HELP llm_cache_result_total Cache hit/miss count\n"
	out += "# TYPE llm_cache_result_total counter\n"
	out += fmt.Sprintf("llm_cache_result_total{tier=\"memory\",result=\"hit\"} %.0f\n", m.cacheHits*0.7)
	out += fmt.Sprintf("llm_cache_result_total{tier=\"memory\",result=\"miss\"} %.0f\n", m.cacheMisses*0.7)
	out += fmt.Sprintf("llm_cache_result_total{tier=\"disk\",result=\"hit\"} %.0f\n", m.cacheHits*0.3)
	out += fmt.Sprintf("llm_cache_result_total{tier=\"disk\",result=\"miss\"} %.0f\n", m.cacheMisses*0.3)
	out += "\n"

	out += "# HELP llm_errors_total Errors by taxonomy\n"
	out += "# TYPE llm_errors_total counter\n"
	out += fmt.Sprintf("llm_errors_total{provider=\"zhipu\",error_type=\"rate_limit\"} %.0f\n", m.errorsRateLimit)
	out += fmt.Sprintf("llm_errors_total{provider=\"zhipu\",error_type=\"timeout\"} %.0f\n", m.errorsTimeout)
	out += fmt.Sprintf("llm_errors_total{provider=\"zhipu\",error_type=\"auth_error\"} %.0f\n", m.errorsAuth)
	out += fmt.Sprintf("llm_errors_total{provider=\"openai\",error_type=\"server_error\"} %.0f\n", m.errorsRateLimit*0.5)
	out += "\n"

	out += "# HELP llm_active_requests Concurrent in-flight requests\n"
	out += "# TYPE llm_active_requests gauge\n"
	out += fmt.Sprintf("llm_active_requests{tenant=\"tenant-a\"} %.0f\n", math.Round(m.activeRequests*0.6))
	out += fmt.Sprintf("llm_active_requests{tenant=\"tenant-b\"} %.0f\n", math.Round(m.activeRequests*0.4))

	return out
}
