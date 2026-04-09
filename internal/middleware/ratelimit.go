package middleware

import (
	"encoding/json"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      float64
	burst    int
}

func NewRateLimiter(rps float64) *RateLimiter {
	burst := int(rps)
	if burst < 1 {
		burst = 1
	}
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rps,
		burst:    burst,
	}
}

func (rl *RateLimiter) getLimiter(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if l, ok := rl.limiters[key]; ok {
		return l
	}

	l := rate.NewLimiter(rate.Limit(rl.rps), rl.burst)
	rl.limiters[key] = l
	return l
}

func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenant := TenantFromContext(r.Context())
			if tenant == "" {
				next.ServeHTTP(w, r)
				return
			}

			limiter := rl.getLimiter(tenant)
			if !limiter.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"message": "Rate limit exceeded. Please retry after a moment.",
						"type":    "rate_limit_error",
						"code":    "rate_limit_exceeded",
					},
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
