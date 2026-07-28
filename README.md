# Distributed Rate Limiter (Go)

A per-user, per-endpoint token bucket rate limiter built from scratch in Go, using sharded storage, lazy refill, and LRU-based memory bounding — designed with production-scale concerns in mind, benchmarked honestly at the scale actually tested.

## Problem Statement

APIs need to limit how often a given user can hit a given endpoint, without the limiter itself becoming a bottleneck or an unbounded memory sink. This project implements a **token bucket** rate limiter that:

- Tracks limits independently per `(userID, endpoint)` pair
- Allows configurable burst capacity and refill rate per endpoint
- Rejects requests instantly (non-blocking) rather than making callers wait
- Computes token refill lazily from elapsed time, rather than running a background timer per bucket
- Bounds memory per shard via LRU eviction, so idle users don't accumulate forever
- Actively expires long-idle buckets via a background TTL sweep
- Is exposed over HTTP via middleware
- Is verified safe under concurrent load using Go's race detector, not just manual testing

## Architecture

### Sharded storage
State is split across a fixed number of shards (currently 256), each with its own mutex and its own bounded map + doubly-linked list. A user+endpoint key is hashed and routed to exactly one shard. This reduces lock contention under concurrent load — unrelated requests routed to different shards never block each other, unlike a single global lock over one shared map.

### Lazy refill (no per-bucket goroutines)
An earlier version of this project used one background goroutine per bucket, each sleeping and waking on its own ticker to add tokens over time. This version replaces that entirely: token count is **computed on read**, from `elapsed time since last touch / configured refill interval`, capped at the bucket's capacity. This removes the overhead of potentially thousands of idle goroutines and eliminates an entire class of goroutine-lifecycle bugs (orphaned goroutines referencing deleted buckets) that the earlier design had to explicitly guard against.

### LRU eviction per shard
Each shard maintains a doubly-linked list ordered by recency of access, alongside its map. When a shard reaches capacity, the least-recently-used entry (the tail of the list) is evicted to make room for a new one. Every successful access moves that entry to the head. This bounds memory per shard regardless of how many distinct users have ever been seen, at the cost of evicting genuinely-idle users' state (acceptable, since an evicted user simply gets treated as new on their next request — they are not being unfairly blocked, just losing any partially-refilled token count they had accumulated).

### TTL background sweep
A background goroutine periodically checks each shard's list, starting from the tail (least recently used) and walking forward. Because the list is already ordered by recency, and all entries share the same TTL, the sweep can stop as soon as it finds an entry that hasn't yet expired — everything ahead of it in the list is guaranteed to be even more recently touched, and therefore also not expired. This avoids a full list scan on every sweep.

## Design Decisions

### Buckets start full, not empty
A newly created bucket has `capacity - 1` tokens immediately (accounting for the token just consumed by the request that created it). This lets a client's first requests use full burst capacity rather than being throttled before doing anything wrong.

### `Allow()` is non-blocking, reject-based
The server does not queue denied requests or make callers wait. Retry/backoff is the client's responsibility. A TTL-based request queue was considered as an alternative (closer to a leaky bucket) but deliberately scoped out in favor of the simpler, more standard reject model used by most real API rate limiters (HTTP 429 + client-side backoff).

### Single write lock per shard, not a read/write split
An earlier version used a read-lock fast path (check existence cheaply) that escalated to a write lock only when creating a new bucket. This had a real concurrency bug: under concurrent first-time access to the same key, one goroutine could fall through the existence check without hitting a `return`, then continue operating on state under a `defer` that was scoped incorrectly relative to the code path taken. Traced and fixed by switching to a single lock held for the full duration of each `Allow()` call. The tradeoff: this removes concurrent-read parallelism for existing buckets within the same shard. Given 256 shards already reduce contention substantially, this is treated as an acceptable simplicity-for-correctness tradeoff — worth revisiting with a correctly-implemented double-checked-locking pattern if shard-level contention shows up in benchmarking.

### Endpoint config uses a simple string-keyed registry, not hashing
An earlier iteration explored using SHA-256 to generate fixed-size endpoint identifiers. This was reconsidered and reverted: for a small, known, non-adversarial set of endpoint routes, a cryptographic hash adds real CPU cost per request and, for short endpoint strings, is actually *larger* than the string it replaces — solving a memory problem by making it worse. The registry now does a direct string-keyed lookup.

## Known Bugs Found and Fixed During Development

1. **Data race on shared config map**: an early version read endpoint config without holding its protecting mutex in one code path while another path wrote to it. Caught by `go test -race`, not by manual testing.
2. **Nil-pointer panic from orphaned goroutines** (pre-lazy-refill design): when a bucket's underlying map got reset while a per-bucket refill goroutine was still running, the goroutine would look up a now-missing key and panic on a nil dereference. Fixed by checking existence before use and exiting the goroutine cleanly. This entire bug class was later eliminated by moving to the lazy-refill design, which removes per-bucket goroutines altogether.
3. **Double-lock / incorrect defer scoping**: see "Single write lock per shard" above.

## Known Limitations / Future Work

- **Middleware currently hardcodes the endpoint** it checks against, rather than deriving it from the incoming request path. Needs to read `r.URL.Path` instead.
- **No `Retry-After` header on rejected requests.** The information needed (refill interval) exists in the endpoint registry but isn't yet surfaced to the HTTP layer.
- **No distributed state.** This implementation is single-process, in-memory. A production deployment behind multiple instances needs shared state — planned next step is a Redis-backed version using atomic Lua scripts for check-and-refill, avoiding the races a naive get-then-set from multiple nodes would introduce.
- **No benchmarks yet.** Real p50/p95/p99 latency and throughput numbers, measured locally with `k6` or `wrk`, are needed before making any concrete performance claims. Any scale claims beyond what's actually measured should be framed as architectural intent, not proven results.
- **Config is static, set at startup.** Runtime-mutable per-endpoint limits would need a design update.
- **Reject-based, not queue-based**, by deliberate choice — see Design Decisions above.

## Testing

All tests pass under Go's race detector:

```bash
go test -v -race -count=1 ./...
```

Coverage includes:
- Burst-then-deny for a single user
- Refill correctness after waiting past the configured interval
- Concurrent correctness (100 goroutines against a single new key, exercising the lock-acquisition path directly)
- Unknown-endpoint fallback (denied, not a crash or a silent misconfiguration)
- LRU eviction under shard capacity pressure
- TTL-based expiry via the background sweep

## Running Locally

```bash
go run main.go
```

```bash
curl -i http://localhost:8080/health
curl -i -H "X-User-Id: 5" http://localhost:8080/limited
```

## Tech Stack

- Go (standard library only — `sync`, `time`, `math`, `net/http`)
- Planned: Redis for distributed state, `k6`/`wrk` for load testing
