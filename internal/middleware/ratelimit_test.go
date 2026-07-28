package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ratelimiter/internal/domain"
	"ratelimiter/internal/limiter"
	"ratelimiter/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
)

func TestRateLimiterMiddleware(t *testing.T) {
	// 1. Setup in-memory Redis
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to start miniredis: %v", err)
	}
	defer mr.Close()

	redisStore := repository.NewRedisStore(mr.Addr())

	// 2. Setup SQL mock
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer db.Close()

	// Inject the mock DB into your PostgresStore struct (requires a minor tweak to NewPostgresStore to accept *sql.DB, or bypassing it for tests)
	// For simplicity in this test, we seed the Registry with memory data directly to bypass the DB fallback
	initialData := map[string]domain.EndpointData{
		"/api/v1/test": {
			Path:             "/api/v1/test",
			RefillWaitTimeMS: 1000,
			MaxLimit:         2,
		},
	}

	registry := limiter.NewRegistry(nil, initialData) // DB is nil because we seeded memory
	rlMiddleware := NewRateLimiter(registry, redisStore)

	// 3. Setup HTTP test recorder and dummy handler
	handler := rlMiddleware.Handle(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-User-Id", "user_123")

	// 4. Execute requests to trigger the limit
	// Request 1: Should pass
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req)
	if rr1.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %v", rr1.Code)
	}

	// Request 2: Should pass
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %v", rr2.Code)
	}

	// Request 3: Should fail (MaxLimit is 2)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req)
	if rr3.Code != http.StatusTooManyRequests {
		t.Errorf("Expected 429 Too Many Requests, got %v", rr3.Code)
	}
}
