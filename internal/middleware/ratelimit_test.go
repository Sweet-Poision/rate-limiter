package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ratelimiter/internal/domain"
	"ratelimiter/internal/limiter"
	"ratelimiter/internal/repository"

	"github.com/alicebob/miniredis/v2"
)

func newTestRegistryAndMiddleware(t *testing.T, initialData map[string]domain.EndpointData) (*RateLimiter, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to start miniredis: %v", err)
	}
	redisStore := repository.NewRedisStore(mr.Addr())
	registry := limiter.NewRegistry(nil, initialData) // nil DB: no fallback query expected in these tests
	return NewRateLimiter(registry, redisStore), mr
}

func TestRateLimiterMiddleware_BasicBurstThenDeny(t *testing.T) {
	initialData := map[string]domain.EndpointData{
		"/api/v1/test": {Path: "/api/v1/test", RefillWaitTimeMS: 1000, MaxLimit: 2},
	}
	rlMiddleware, mr := newTestRegistryAndMiddleware(t, initialData)
	defer mr.Close()

	handler := rlMiddleware.Handle(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-User-Id", "user_123")

	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("call %d: expected 200 OK, got %v", i+1, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 on 3rd call, got %v", rr.Code)
	}
}

// TestRefillMathIsCorrect exercises the actual Lua script's refill formula by
// draining a bucket, advancing miniredis's internal clock by a known amount,
// and checking that only the expected number of tokens have been replenished
// — not fewer (starved) and not the whole bucket at once (the bug we found,
// where `elapsed * (max_limit / refill_rate_ms)` refilled almost instantly).
func TestRefillMathIsCorrect(t *testing.T) {
	initialData := map[string]domain.EndpointData{
		// 1 token every 100ms, capacity 5 — after advancing 250ms we expect
		// roughly 2 tokens refilled (250 / 100 = 2.5, floored by the >=1 check),
		// NOT all 5 (which the buggy formula would have produced almost
		// instantly regardless of elapsed time).
		"/api/v1/refill-test": {Path: "/api/v1/refill-test", RefillWaitTimeMS: 100, MaxLimit: 5},
	}
	rlMiddleware, mr := newTestRegistryAndMiddleware(t, initialData)
	defer mr.Close()

	handler := rlMiddleware.Handle(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/refill-test", nil)
	req.Header.Set("X-User-Id", "refill_user")

	// Drain the bucket completely (5 tokens).
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("drain call %d: expected 200, got %v", i+1, rr.Code)
		}
	}

	// Confirm fully drained.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after draining, got %v", rr.Code)
	}

	// Advance miniredis's clock by 250ms — real wall-clock time is used by
	// the Lua script (time.Now() in Go), so we sleep instead of using
	// miniredis.FastForward (which only affects Redis-native TTL/expiry,
	// not values our own script reads via UnixMilli()).
	time.Sleep(250 * time.Millisecond)

	allowedCount := 0
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			allowedCount++
		}
	}

	// Expect exactly 2 tokens refilled from 250ms elapsed at 1 token/100ms.
	// A tolerance of 1 either way accounts for real-clock timing jitter in
	// the test itself (sleep is not perfectly precise).
	if allowedCount < 1 || allowedCount > 3 {
		t.Errorf("expected ~2 tokens refilled after 250ms (got range 1-3 tolerance), got %d allowed", allowedCount)
	}
	if allowedCount == 5 {
		t.Errorf("all 5 tokens were available after only 250ms — this is the exact bug where refill ignores elapsed time and fills the bucket almost instantly")
	}
}

// Negative-cache behavior (DB miss -> cached rejection -> no repeated DB
// hits) is covered without skipping in internal/limiter/registry_test.go,
// using sqlmock to inject a fake DB. That's a better fit than testing it
// here through full HTTP, since it can assert exact DB query counts.

func TestClientIdentifier_StripsPortFromRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:54821"

	id := clientIdentifier(req)
	if id != "203.0.113.5" {
		t.Errorf("expected IP without port, got %q", id)
	}
}

func TestClientIdentifier_PrefersXUserIdHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:54821"
	req.Header.Set("X-User-Id", "authenticated_user_42")

	id := clientIdentifier(req)
	if id != "authenticated_user_42" {
		t.Errorf("expected header value to take priority, got %q", id)
	}
}

func TestClientIdentifier_SameIPDifferentPortsProducesSameKey(t *testing.T) {
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "203.0.113.5:11111"

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "203.0.113.5:99999"

	id1 := clientIdentifier(req1)
	id2 := clientIdentifier(req2)

	if id1 != id2 {
		t.Errorf("expected same IP with different ports to produce the same rate-limit key, got %q vs %q", id1, id2)
	}
}
