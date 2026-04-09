package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mattemmons/sentinel/internal/auth"
)

type ctxKey string

const (
	tenantKey ctxKey = "tenant"
	modelKey  ctxKey = "model"
)

func ContextWithTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, tenantKey, tenant)
}

func ContextWithModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, modelKey, model)
}

func TenantFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(tenantKey).(string); ok {
		return v
	}
	return ""
}

func ModelFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(modelKey).(string); ok {
		return v
	}
	return ""
}

func Auth(ks *auth.KeyStore, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := auth.ExtractAPIKey(r)
			if key == "" {
				writeAuthError(w, "Missing API key. Provide Authorization: Bearer <key> or ?key=<key>.")
				return
			}

			tenant, ok := ks.Validate(key)
			if !ok {
				logger.Warn("invalid API key", "remote_addr", r.RemoteAddr)
				writeAuthError(w, "Invalid API key.")
				return
			}

			ctx := ContextWithTenant(r.Context(), tenant)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    "authentication_error",
			"code":    "invalid_api_key",
		},
	})
}
