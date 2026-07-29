# Distributed Rate Limiter (Go + Redis + Postgres)

A per-user, per-endpoint token bucket rate limiter, backed by Redis for
shared state across multiple instances and Postgres for endpoint config —
built and tested for the case that actually matters for a rate limiter:
multiple independent processes enforcing one shared limit correctly.

## Problem Statement

APIs need to limit how often a given user can hit a given endpoint,
without the limiter itself becoming a bottleneck, a single point of
failure, or inconsistent once you're running more than one instance.
This project implements a **token bucket** rate limiter that:

- Tracks limits independently per `(userID, endpoint)` pair
- Allows configurable burst capacity and refill rate per endpoint,
  defined in Postgres and loaded/cached per instance
- Rejects requests instantly (non-blocking) rather than making callers wait
- Computes token refill lazily from elapsed time inside a single atomic
  Redis Lua script — no per-request read-then-write race between instances
- Is exposed over HTTP via middleware, keyed off the actual request path
- Is verified correct under real concurrent, multi-process load — not
  just `go test -race` against one process (see Distributed Testing below)

## Architecture

### Redis-backed shared state (the core correctness property)
All rate-limit state — token count and last-refill timestamp — lives in
Redis, not in any single instance's memory. Every check-and-refill
happens inside one Lua script (`internal/repository/redis.go`), executed
atomically by Redis: read current tokens, compute refill from elapsed
time, decide allow/deny, write back — all as one operation Redis
guarantees can't interleave with another instance's script run on the
same key. This is what makes it safe to run N instances behind a load
balancer without them stepping on each other's counts.

### Endpoint config: Postgres + in-memory cache + pubsub updates
`internal/limiter/registry.go` holds a per-instance in-memory cache of
`(path → limit config)`, loaded from Postgres at startup. On a cache
miss it queries Postgres (via `singleflight`, so concurrent misses for
the same path collapse into one query), and caches a **negative** result
for 30s so a mistyped/unknown path doesn't hammer Postgres on every
request. Updates published to Redis pub/sub (`endpoint_updates` channel)
are applied live to every instance's cache without a restart.

### Non-blocking, reject-based
The server does not queue denied requests or make callers wait.
Retry/backoff is the client's responsibility — the standard model most
real API rate limiters use (HTTP 429 + client-side backoff), rather than
a queue-based/leaky-bucket approach.

## Known Bugs Found and Fixed During Development

1. **Refill formula bug in the Lua script**: an earlier version computed
   refill as `elapsed * (max_limit / refill_rate_ms)`, which made the
   refill amount scale with `max_limit` instead of being independent of
   it — a high-capacity bucket would refill to full in ~1ms regardless
   of real elapsed time. Fixed to `elapsed_ms / refill_rate_ms` (one
   token per `refill_rate_ms` of elapsed time), matching the intended
   semantics regardless of bucket size.
2. **IP-based fallback using the wrong value**: `RemoteAddr` includes an
   ephemeral per-connection port that differs on every request, making
   raw `RemoteAddr` useless as a stable rate-limit key for anonymous
   traffic. Fixed via `net.SplitHostPort` to key on the host only.

## Known Limitations / Future Work

- **`Retry-After` header is hardcoded to `1` second** on rejected
  requests, rather than being derived from the endpoint's actual
  `refill_wait_time_ms`. The information already exists in the endpoint
  registry; the middleware just isn't surfacing it yet.
- **Config is static once cached**, refreshed only via explicit pubsub
  updates — there's no polling fallback if a pubsub message is dropped
  (Redis pub/sub is fire-and-forget, not guaranteed delivery).
- **Reject-based, not queue-based**, by deliberate choice — see
  Design Decisions above.
- **Single Redis instance is a single point of failure.** A dead Redis
  currently fails closed (`503 Rate limiter unavailable` — see
  `middleware/ratelimit.go`), which is the safe default, but there's no
  Redis Sentinel/Cluster setup yet for HA.

## Testing

Unit/integration tests run against a real Redis protocol implementation
(`miniredis`), exercising the actual Lua script — not a mock of the
rate-limit logic:

```bash
go test -v -race -count=1 ./...
```

### Distributed testing (Kubernetes, local)

Beyond unit tests, this was validated under real multi-process
concurrency: 3 replicas of the app, one shared Redis, deployed to a
local Kubernetes cluster (see `k8s/SETUP.md`).

**Sustained load, load-balanced across all 3 pods** (`k6`, ~1.15M
requests, mixed authenticated/anonymous/unknown-endpoint traffic):
- 0 request failures, 100% of checks passed
- 25,741 req/s sustained, p95 latency 31.5ms

**Deterministic single-user correctness check** — one user, one endpoint
seeded with `max_limit=5`, 21 near-simultaneous requests fired across
all 3 pods:
- Result: exactly 5 `200`s and 16 `429`s — the limit held as *one*
  shared count across 3 independent processes, not `5 × 3`. This is the
  specific property the Redis Lua script exists to guarantee, confirmed
  under real concurrent multi-pod load rather than assumed from the
  design.

## Running Locally

```bash
docker compose up -d          # starts Redis + Postgres
go run scripts/seed.go        # migrates + seeds endpoint configs
go run cmd/api/main.go
```

```bash
curl -i http://localhost:8080/health
curl -i -H "X-User-Id: 5" http://localhost:8080/api/v1/auth/profile/read
```

## Running on Kubernetes (local)

See `k8s/SETUP.md` for a full walkthrough (OrbStack-specific, but
adaptable to any local cluster) — builds the image, deploys Postgres,
Redis, and 3 app replicas, seeds the database, and runs both a
sustained load test and the deterministic correctness check above.

## Tech Stack

- Go — `net/http`, `sync`, `database/sql`
- Redis (`go-redis/v9`) — shared rate-limit state via atomic Lua script
- Postgres (`lib/pq`) — endpoint configuration
- `golang.org/x/sync/singleflight` — collapses concurrent cache-miss queries
- `prometheus/client_golang` — `/metrics` endpoint
- `miniredis` — real-protocol Redis testing without a live server
- `k6` — load testing, both single-process and distributed (see above)
