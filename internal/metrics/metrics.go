package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total requests processed, labeled by outcome (allowed, rate_limited, not_found, error)",
		},
		[]string{"outcome"},
	)

	PositiveCacheHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_registry_positive_cache_hits_total",
			Help: "Endpoint config lookups served from the in-memory positive cache",
		},
	)

	NegativeCacheHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_registry_negative_cache_hits_total",
			Help: "Endpoint lookups short-circuited by the negative cache (known-missing paths)",
		},
	)

	DBQueriesTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_registry_db_queries_total",
			Help: "Actual queries sent to Postgres for endpoint config (should stay low relative to RequestsTotal if caching is working)",
		},
	)

	RedisEvalDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "gateway_redis_eval_duration_seconds",
			Help:    "Latency of the Lua rate-limit script execution in Redis",
			Buckets: prometheus.DefBuckets,
		},
	)
)
