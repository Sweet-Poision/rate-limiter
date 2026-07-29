package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"ratelimiter/internal/config"
	"ratelimiter/internal/domain"
	"ratelimiter/internal/limiter"
	"ratelimiter/internal/middleware"
	"ratelimiter/internal/repository"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func limitedHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Request successful! Rate limit passed."))
}

// connectWithRetry attempts NewPostgresStore repeatedly with exponential
// backoff, instead of failing on the first attempt. This matters
// specifically in Kubernetes (or any orchestrated environment): there is no
// guarantee the Postgres pod is accepting connections yet just because it
// was applied first — kubectl apply order does not imply readiness order.
// A single-shot connection attempt turns a normal, temporary startup race
// into a hard crash loop.
func connectWithRetry(dbURL string, maxAttempts int) (*repository.PostgresStore, error) {
	var lastErr error
	backoff := 1 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		store, err := repository.NewPostgresStore(dbURL)
		if err == nil {
			return store, nil
		}

		lastErr = err
		log.Printf("Postgres connection attempt %d/%d failed: %v (retrying in %v)", attempt, maxAttempts, err, backoff)

		time.Sleep(backoff)

		// Exponential backoff, capped at 30s so we're not waiting forever
		// between attempts on a genuinely long outage.
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}

	return nil, fmt.Errorf("failed to connect to database after %d attempts: %w", maxAttempts, lastErr)
}

func main() {
	cfg := config.Load()

	// 10 attempts with exponential backoff (1s, 2s, 4s, 8s, 16s, 30s, 30s...)
	// gives well over two minutes of tolerance for a slow-starting Postgres
	// pod, while still eventually failing loudly if something is genuinely
	// broken rather than just slow.
	dbStore, err := connectWithRetry(cfg.DatabaseURL, 10)
	if err != nil {
		log.Fatalf("Giving up on database connection: %v", err)
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
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/api/", rlMiddleware.Handle(limitedHandler))

	fmt.Printf("Server listening on port %s...\n", cfg.ServerPort)
	log.Fatal(http.ListenAndServe(":"+cfg.ServerPort, nil))
}
