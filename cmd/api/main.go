package main

import (
	"fmt"
	"log"
	"net/http"
	"ratelimiter/server/middleware"
	"ratelimiter/server/ratelimiter"

	"github.com/joho/godotenv"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func limitedHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Request successsful! Rate limit passed."))
}

func main() {

	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found. Relying on system environment variables.")
	}

	ratelimiter.InitDB()
	ratelimiter.InitRedis()

	ratelimiter.RateLimiter()

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/limited", middleware.RateLimitingMiddleware(limitedHandler))

	fmt.Println("Server listening on port 8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
