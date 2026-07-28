package middleware

import (
	"net/http"
	"ratelimiter/server/ratelimiter"
	"strconv"
)

func RateLimitingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userIdStr := r.Header.Get("X-User-Id")
		userId, err := strconv.Atoi(userIdStr)
		if err != nil {
			userId = 1
		}
		endpoint := "api/v1/health1"

		if ratelimiter.Allow(userId, endpoint) {
			next.ServeHTTP(w, r)
		} else {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		}
	}
}
