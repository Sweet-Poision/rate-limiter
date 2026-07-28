package main

import (
	"fmt"
	"log"
	"net/http"
	"ratelimiter/server/middleware"
	"ratelimiter/server/ratelimiter"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func limitedHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("you got through"))
}

func main() {
	ratelimiter.RateLimiter()
	ratelimiter.InitRedis()
	ratelimiter.TestRedis()

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/limited", middleware.RateLimitingMiddleware(limitedHandler))

	fmt.Println("Server listening on port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
