package ratelimiter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setup initializes the rate limiter before standard tests using the default configuration.
func setup() {
	RateLimiter()
}

func TestBurstThenDeny(t *testing.T) {
	setup()
	userId := 1
	endpoint := "api/v1/health1" // Capacity: 5, Refill: 100ms

	for i := 0; i < 5; i++ {
		if !Allow(userId, endpoint) {
			t.Fatalf("Expected call %d to be Allowed, but it was denied", i+1)
		}
	}

	if Allow(userId, endpoint) {
		t.Fatalf("Expected call 6 to be denied, but it was Allowed")
	}
}

func TestRefillActuallyWorks(t *testing.T) {
	setup()
	userId := 2
	endpoint := "api/v1/health1"

	for i := 0; i < 5; i++ {
		Allow(userId, endpoint)
	}

	if Allow(userId, endpoint) {
		t.Fatal("Expected to be denied after draining bucket")
	}

	time.Sleep(time.Millisecond * 125)

	if !Allow(userId, endpoint) {
		t.Fatal("Expected to be Allowed after refill, but was denied")
	}
}

func TestConcurrentAccess(t *testing.T) {
	setup()
	userId := 3
	endpoint := "api/v1/health1"

	var wg sync.WaitGroup
	var successCount int64 = 0
	goroutineCount := 100

	wg.Add(goroutineCount)
	for i := 0; i < goroutineCount; i++ {
		go func() {
			defer wg.Done()
			if Allow(userId, endpoint) {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	if successCount != 5 {
		t.Fatalf("Expected exactly 5 successful requests, but got %d", successCount)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	setup()
	userId := 4

	if Allow(userId, "api/v1/unknown") {
		t.Fatal("Expected request to unknown endpoint to be denied, but it was Allowed")
	}
}

// TestLRUCapacityEviction verifies that when a shard reaches capacity,
// the least recently used (LRU) user is evicted to make room for a new user.
func TestLRUCapacityEviction(t *testing.T) {
	// Custom isolated setup
	started = false
	shardMaps = NewShardedStorage(1, 2) // Force 1 shard with a max capacity of 2 users
	availableEndpoints = safeEndpointRegistry{
		endpoints: make(map[string]endpointData),
	}
	availableEndpoints.endpoints["api/v1/test"] = endpointData{refilWaitTimeMS: 100, maxLimitBucket: 5}
	started = true

	endpoint := "api/v1/test"

	// 1. Add User 1 and User 2
	Allow(1, endpoint)
	time.Sleep(10 * time.Millisecond) // Guarantee timestamp difference
	Allow(2, endpoint)
	time.Sleep(10 * time.Millisecond)

	// 2. Touch User 1. This updates their lastTouchTime and moves them to the head of the DLL.
	Allow(1, endpoint)
	time.Sleep(10 * time.Millisecond)

	// 3. Add User 3. Capacity is 2, so one user must be evicted.
	// Because User 1 was touched recently, User 2 is now the LRU at the tail.
	Allow(3, endpoint)

	targetShard := &shardMaps.shards[0]
	targetShard.mu.RLock()
	_, hasUser1 := targetShard.bucket[key{1, endpoint}]
	_, hasUser2 := targetShard.bucket[key{2, endpoint}]
	_, hasUser3 := targetShard.bucket[key{3, endpoint}]
	currentSize := targetShard.size
	targetShard.mu.RUnlock()

	if currentSize != 2 {
		t.Fatalf("Expected shard size 2, got %d", currentSize)
	}
	if !hasUser1 {
		t.Fatal("Expected User 1 to be retained due to LRU move-to-head")
	}
	if hasUser2 {
		t.Fatal("Expected User 2 to be evicted as the tail node")
	}
	if !hasUser3 {
		t.Fatal("Expected User 3 to be present")
	}
}

// TestTTLBackgroundWorker verifies that the background goroutine successfully
// evaluates timestamps and removes expired nodes without full list traversals.
func TestTTLBackgroundWorker(t *testing.T) {
	// Custom isolated setup
	started = false
	shardMaps = NewShardedStorage(1, 100)
	availableEndpoints = safeEndpointRegistry{
		endpoints: make(map[string]endpointData),
	}
	availableEndpoints.endpoints["api/v1/test"] = endpointData{refilWaitTimeMS: 100, maxLimitBucket: 5}
	started = true

	endpoint := "api/v1/test"

	// Add User 1
	Allow(1, endpoint)

	// Start worker with aggressive test timings: check every 50ms, expire nodes older than 100ms
	StartTTLWorker(50*time.Millisecond, 100)

	// Sleep long enough for the user to expire and the worker to execute at least once
	time.Sleep(200 * time.Millisecond)

	targetShard := &shardMaps.shards[0]
	targetShard.mu.RLock()
	_, exists := targetShard.bucket[key{1, endpoint}]
	currentSize := targetShard.size
	targetShard.mu.RUnlock()

	if exists {
		t.Fatal("Expected User 1 to be evicted by the TTL worker, but they are still in the map")
	}
	if currentSize != 0 {
		t.Fatalf("Expected shard size to be 0, got %d", currentSize)
	}
}
