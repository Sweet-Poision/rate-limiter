package ratelimiter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setup() {
	mu.Lock()
	mp = make(map[int]chan struct{})
	started = true
	mu.Unlock()
}

func TestBurstThenDeny(t *testing.T) {
	setup()
	userId := 1

	for i := 0; i < MAX_LIMIT_BUCKET; i++ {
		if !allow(userId) {
			t.Fatalf("Expected call %d to be allowed, but it was denied", i+1)
		}
	}

	if allow(userId) {
		t.Fatalf("Expected call %d to be denied, but it was allowed", MAX_LIMIT_BUCKET+1)
	}
}

func TestRefillActuallyWorks(t *testing.T) {
	setup()
	userId := 2

	for i := 0; i < MAX_LIMIT_BUCKET; i++ {
		allow(userId)
	}

	if allow(userId) {
		t.Fatal("Expected to be denied after draining bucket")
	}

	time.Sleep(time.Millisecond * time.Duration(REFILL_WAIT_TIME_MS) * 2)
	refill()

	if !allow(userId) {
		t.Fatal("Expected to be allowed after refill, but was denied")
	}
}

func TestConcurrentAccess(t *testing.T) {
	setup()
	userId := 0

	var wg sync.WaitGroup
	var successCount int64 = 0
	goroutingCount := 100
	wg.Add(goroutingCount)
	for i := 0; i < goroutingCount; i++ {
		go func() {
			defer wg.Done()
			if allow(userId) {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	wg.Wait()
	if successCount != int64(MAX_LIMIT_BUCKET) {
		t.Fatalf("Expected exactly %d successful requests, but got %d", MAX_LIMIT_BUCKET, successCount)
	}
}
