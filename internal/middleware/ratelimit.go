package middleware

import (
	"database/sql"
	"net"
	"net/http"
	"ratelimiter/internal/limiter"
	"ratelimiter/internal/metrics"
	"ratelimiter/internal/repository"
)

type RateLimiter struct {
	registry *limiter.Registry
	redis    *repository.RedisStore
}

func NewRateLimiter(registry *limiter.Registry, redis *repository.RedisStore) *RateLimiter {
	return &RateLimiter{
		registry: registry,
		redis:    redis,
	}
}

// clientIdentifier extracts a best-effort identity for rate limiting.
// Prefers an authenticated X-User-Id header; falls back to the client's IP
// (NOT RemoteAddr directly, which includes an ephemeral per-connection port
// that differs on every request, making it useless as a rate-limit key).
func clientIdentifier(r *http.Request) string {
	userID := r.Header.Get("X-User-Id")
	if userID != "" {
		return userID
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // fallback if RemoteAddr has no port
	}
	return host
}

func (rl *RateLimiter) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		endpointConfig, err := rl.registry.GetEndpoint(path)
		if err != nil {
			if err == sql.ErrNoRows {
				metrics.RequestsTotal.WithLabelValues("not_found").Inc()
				http.Error(w, "Endpoint not registered", http.StatusNotFound)
				return
			}
			metrics.RequestsTotal.WithLabelValues("error").Inc()
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		userID := clientIdentifier(r)

		allowed, err := rl.redis.EvaluateRateLimit(r.Context(), userID, endpointConfig)
		if err != nil {
			metrics.RequestsTotal.WithLabelValues("error").Inc()
			http.Error(w, "Rate limiter unavailable", http.StatusServiceUnavailable)
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", "1")
			metrics.RequestsTotal.WithLabelValues("rate_limited").Inc()
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		metrics.RequestsTotal.WithLabelValues("allowed").Inc()
		next(w, r)
	}
}
