package ratelimiter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setup initializes the rate limiter before each test
func setup() {
	mu.Lock()
	mp = make(map[key]chan struct{})
	started = true
	mu.Unlock()

	epName := "api/v1/test"
	id := 1

	timeMapMu.Lock()
	timeMap = make(map[int]tokenData)
	timeMap[id] = tokenData{refilWaitTimeMS: 50, maxLimitBucket: 5}
	timeMapMu.Unlock()

	epMu.Lock()
	endpoints = make(map[string]int)
	endpoints[epName] = id
	epMu.Unlock()
}

func TestBurstThenDeny(t *testing.T) {
	setup()
	userId := 1
	endpoint := "api/v1/test"

	// 1. Consume the exact burst limit (5)
	for i := 0; i < 5; i++ {
		if !Allow(userId, endpoint) {
			t.Fatalf("Expected call %d to be Allowed, but it was denied", i+1)
		}
	}

	// 2. The 6th call should immediately fail (0 TTL)
	if Allow(userId, endpoint) {
		t.Fatalf("Expected call 6 to be denied, but it was Allowed")
	}
}

func TestRefillActuallyWorks(t *testing.T) {
	setup()
	userId := 2
	endpoint := "api/v1/test"

	// 1. Drain the bucket
	for i := 0; i < 5; i++ {
		Allow(userId, endpoint)
	}

	// 2. Verify we are denied
	if Allow(userId, endpoint) {
		t.Fatal("Expected to be denied after draining bucket")
	}

	// 3. Wait slightly longer than the 50ms refill time
	time.Sleep(time.Millisecond * 75)

	// 4. Verify we are Allowed again
	if !Allow(userId, endpoint) {
		t.Fatal("Expected to be Allowed after refill, but was denied")
	}
}

func TestConcurrentAccess(t *testing.T) {
	setup()
	userId := 3
	endpoint := "api/v1/test"

	var wg sync.WaitGroup
	var successCount int64 = 0
	goroutineCount := 100

	wg.Add(goroutineCount)
	for i := 0; i < goroutineCount; i++ {
		go func() {
			defer wg.Done()
			// Using 0 TTL so requests fail immediately if no token
			if Allow(userId, endpoint) {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	// Since the bucket starts full with 5, exactly 5 should succeed
	if successCount != 5 {
		t.Fatalf("Expected exactly 5 successful requests, but got %d", successCount)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	setup()
	userId := 4

	// This endpoint does not exist in our maps
	if Allow(userId, "api/v1/unknown") {
		t.Fatal("Expected request to unknown endpoint to be denied, but it was Allowed")
	}
}