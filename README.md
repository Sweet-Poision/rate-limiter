# Distributed Rate Limiter (Go)

A per-user, per-endpoint token bucket rate limiter built from scratch in Go, focused on correctness under concurrency over feature breadth.

## Problem Statement

APIs need to limit how often a given user can hit a given endpoint, without becoming a bottleneck themselves. This project implements a **token bucket** rate limiter that:

- Tracks limits independently per `(userID, endpoint)` pair
- Allows configurable burst capacity and refill rate per endpoint
- Rejects requests instantly (non-blocking) rather than making callers wait
- Is safe under high concurrent load (verified with Go's race detector, not just manual testing)

## Design Decisions

Every major decision below was made deliberately, with tradeoffs considered — not defaults.

### 1. Buckets start full, not empty
A newly created bucket is filled to capacity immediately. This lets a client's very first requests use full burst capacity rather than being artificially throttled before they've done anything wrong. Burst capacity is a deliberate feature of a token bucket, not a leak in the design — it allows short spikes while still enforcing a long-term average rate via refill.

### 2. `Allow()` is non-blocking
A rate limiter's job is to say "yes, proceed" or "no, back off" instantly. It is not a queueing system. If `Allow()` blocked a rejected caller, it would just relocate the problem (a stuck goroutine, a stalled HTTP handler) instead of solving it. The decision to retry, and how, belongs to the client — the server should not hold state or a connection open on the client's behalf.

**Considered and rejected for now:** a TTL-based request queue (closer to a leaky bucket), where denied requests wait in a queue with an expiry instead of being rejected outright. This is a legitimate, more complex alternative — noted here as a scoped-out design, not an oversight. See "Future Work."

### 3. Refill skips (does not block) when the bucket is already full
The background refill process tries to add a token via a non-blocking channel send (`select`/`default`). If the bucket is already full, it simply skips that tick instead of blocking. A blocking refill could stall the refill goroutine indefinitely under certain conditions — skipping keeps it always making forward progress.

### 4. Per-endpoint configuration, per-bucket refill goroutines
Different endpoints warrant different limits — for both business reasons (a login endpoint needs tighter limits to resist brute-forcing than a health check) and load reasons. Because refill *rates* can now differ per endpoint, a single shared/global ticker can't correctly serve all buckets — a fast-refill endpoint and a slow-refill endpoint can't share one tick rate without one of them being served incorrectly.

**Solution chosen:** each bucket gets its own dedicated background goroutine, running its own `time.Ticker` at that endpoint's configured interval. This is a deliberate, idiomatic-Go tradeoff — goroutines are cheap (KBs of stack, not OS threads), so thousands of independent per-bucket tickers is a normal pattern, not overengineering.

**Considered and rejected for now:** a single global scheduler using a priority queue (ordered by next-refill-time) with an interruptible sleep (`select` racing a timer against a "wake early" signal channel). This is the more optimal approach at very large scale (avoids a goroutine per bucket entirely) but is meaningfully more complex to implement correctly. Noted as a future optimization.

### 5. Composite struct key for the bucket map
`map[struct{ userID int; endpoint string }]*bucket` is used directly, rather than a hand-built string key (e.g., concatenating userID and endpoint). Go allows structs to be used as map keys natively as long as all fields are comparable, which sidesteps manual encoding/hashing and any collision risk from string concatenation, with no extra code required.

### 6. Two separate mutexes for two separate pieces of shared state
`mu` protects the bucket map (`mp`); `timeMapMu` protects the per-endpoint config map (`timeMap`). Keeping them separate avoids unnecessary contention between unrelated operations (e.g., a config lookup doesn't need to wait on a bucket-map write, and vice versa).

## Known Bugs Found and Fixed During Development

Documented because the debugging process itself is part of the engineering story:

1. **Data race on `timeMap`**: an early version read `timeMap` inside `refill()` without holding `timeMapMu`, while a test's `setup()` concurrently wrote to it. Caught by `go test -race`, not by manual testing — a good illustration of why the race detector matters over just eyeballing output across multiple runs.
2. **Nil-pointer panic from orphaned goroutines**: when a bucket's underlying map (`mp`) gets reset (e.g., during test setup, or conceptually during a config reload in production), any `refill` goroutine still running for a now-deleted key would look up a missing key, get Go's zero value (`nil`) for the pointer, and panic on the next dereference. Fixed by checking the second return value (`val, ok := mp[userKey]`) and exiting the goroutine cleanly if the bucket no longer exists.

## Known Limitations / Future Work

- **Goroutine cleanup is reactive, not proactive.** A bucket's `refill` goroutine only notices its bucket is gone on its *next* scheduled wake-up, not immediately. At small scale this is negligible; at very large scale with high bucket churn, a `context.Context`-based cancellation signal or a TTL-based sweep would be a more correct fix.
- **No distributed state yet.** This implementation is single-process, in-memory. A production deployment behind multiple instances needs shared state — the planned next step is a Redis-backed version using atomic Lua scripts for the check-and-increment operation, avoiding the race conditions a naive get-then-set from multiple nodes would introduce.
- **Config is static and set at startup.** Runtime-mutable per-endpoint limits (e.g., an ops team adjusting a limit without redeploying) would need either a re-read-on-each-check design or the interruptible-sleep pattern mentioned above so already-running refill goroutines can pick up new intervals immediately.
- **Reject-based, not queue-based.** As above — a TTL-queue alternative was considered and deliberately scoped out in favor of a simpler, more standard reject-and-let-the-client-retry model (the same approach used by most real API rate limiters, e.g., HTTP 429 + `Retry-After`).
- **No HTTP layer yet.** Currently a library, not a runnable service. Next step: wrap `Allow()` in HTTP middleware so it's demoable via `curl`.

## Testing

All tests pass under Go's race detector:

```bash
go test -v -race ./...
```

Coverage includes:
- **Burst-then-deny**: a single user can consume exactly up to capacity, then is denied
- **Refill correctness**: after being denied, waiting past the configured refill interval allows exactly one more request
- **Concurrent correctness**: 100 goroutines hitting `Allow()` simultaneously for a brand-new key results in exactly `capacity` successes — this is the test that actually exercises the double-checked locking around bucket creation, and would catch a regression there under `-race`
- **Unknown endpoint fallback**: a request to an endpoint with no configured limit is denied rather than silently misbehaving

## Tech Stack

- Go (standard library only — `sync`, `time`, no external dependencies yet)
- Planned: Redis for distributed state, `net/http` for middleware
