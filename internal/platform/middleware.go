package platform

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// RequestLogger emits a structured slog line per request with method, path,
// status, and duration. Sensitive path segments (e.g. calendar ICS tokens)
// are redacted before logging.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			defer func() {
				logger.Info("http_request",
					"method", r.Method,
					"path", redactLogPath(r.URL.Path),
					"status", ww.Status(),
					"bytes", ww.BytesWritten(),
					"duration_ms", time.Since(start).Milliseconds(),
					"request_id", middleware.GetReqID(r.Context()),
				)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// redactLogPath masks secrets embedded in URL paths before they reach logs.
func redactLogPath(path string) string {
	if !strings.HasPrefix(path, "/v1/cal/") {
		return path
	}
	parts := strings.Split(path, "/")
	// /v1/cal/{user_id}/{token} or .../{token}.ics
	if len(parts) >= 5 && parts[4] != "" {
		parts[4] = "[redacted]"
		return strings.Join(parts, "/")
	}
	return path
}

// CORS allows the SPA dev origin with credentials so the httpOnly refresh
// cookie flows. In production the SPA is same-origin behind Caddy.
func CORS(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization,Content-Type,If-Match,Idempotency-Key")
			h.Set("Access-Control-Expose-Headers", "ETag")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
