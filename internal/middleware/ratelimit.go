package middleware

import (
	"database/sql"
	"net/http"
	"ratelimiter/server/ratelimiter"
)

func RateLimitingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		endpointConfig, err := ratelimiter.GetEndpoint(path)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "Endpoint not registered", http.StatusNotFound)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}

		userId := r.Header.Get("X-User-Id")
		if userId == "" {
			userId = r.RemoteAddr
		}

		allowed, err := ratelimiter.EvaluateRateLimit(userId, path, endpointConfig)

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
