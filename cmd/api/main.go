package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"ratelimiter/internal/config"
	"ratelimiter/internal/domain"
	"ratelimiter/internal/limiter"
	"ratelimiter/internal/middleware"
	"ratelimiter/internal/repository"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func limitedHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Request successful! Rate limit passed."))
}

func main() {
	cfg := config.Load()

	dbStore, err := repository.NewPostgresStore(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	redisStore := repository.NewRedisStore(cfg.RedisAddr)

	initialEndpoints, err := dbStore.LoadAllEndpoints()
	if err != nil {
		log.Fatalf("Failed to load initial endpoints: %v", err)
	}

	registry := limiter.NewRegistry(dbStore, initialEndpoints)

	updateChan := make(chan domain.EndpointData)
	go redisStore.ListenForUpdates(context.Background(), updateChan)
	go registry.HandlePubSubUpdates(updateChan)

	rlMiddleware := middleware.NewRateLimiter(registry, redisStore)

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/api/", rlMiddleware.Handle(limitedHandler))

	fmt.Printf("Server listening on port %s...\n", cfg.ServerPort)
	log.Fatal(http.ListenAndServe(":"+cfg.ServerPort, nil))
}
