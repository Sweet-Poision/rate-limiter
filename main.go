package main

import (
	"fmt"
	"log"
	"net/http"
	"ratelimiter/ratelimiter"
	"strconv"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func limitedHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("you got through"))
}

func rateLimitingMiddleware(next http.HandlerFunc) http.HandlerFunc {
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

func main() {
	ratelimiter.RateLimiter()

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/limited", rateLimitingMiddleware(limitedHandler))

	fmt.Println("Server listening on port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
