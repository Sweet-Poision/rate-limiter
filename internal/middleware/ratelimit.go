package middleware

import (
	"database/sql"
	"net/http"

	"ratelimiter/internal/limiter"
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

func (rl *RateLimiter) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		endpointConfig, err := rl.registry.GetEndpoint(path)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "Endpoint not registered", http.StatusNotFound)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		userID := r.Header.Get("X-User-Id")
		if userID == "" {
			userID = r.RemoteAddr
		}

		allowed, err := rl.redis.EvaluateRateLimit(r.Context(), userID, endpointConfig)
		if err != nil {
			http.Error(w, "Rate limiter unavailable", http.StatusServiceUnavailable)
			return
		}

		if !allowed {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}
