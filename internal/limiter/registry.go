package limiter

import (
	"database/sql"
	"sync"
	"time"

	"ratelimiter/internal/domain"
	"ratelimiter/internal/metrics"
	"ratelimiter/internal/repository"

	"golang.org/x/sync/singleflight"
)

// negativeCacheTTL controls how long a "path not found" result is remembered
// before we allow a fresh DB check. Without this, singleflight only collapses
// concurrent misses that overlap in time — it does nothing to stop repeated
// misses spread out over time (e.g. 1000 req/s to a typo'd endpoint would
// still be ~1000 DB queries/s without a negative cache).
const negativeCacheTTL = 30 * time.Second

type Registry struct {
	mu            sync.RWMutex
	endpoints     map[string]domain.EndpointData
	negativeCache map[string]time.Time // path -> when it was marked missing
	db            *repository.PostgresStore
	sfGroup       singleflight.Group
}

func NewRegistry(db *repository.PostgresStore, initialData map[string]domain.EndpointData) *Registry {
	return &Registry{
		endpoints:     initialData,
		negativeCache: make(map[string]time.Time),
		db:            db,
	}
}

func (r *Registry) GetEndpoint(path string) (domain.EndpointData, error) {
	r.mu.RLock()
	data, exists := r.endpoints[path]
	negAt, isNegative := r.negativeCache[path]
	r.mu.RUnlock()

	if exists {
		metrics.PositiveCacheHits.Inc()
		return data, nil
	}

	// Negative cache hit: reject instantly, no DB call, no singleflight needed.
	if isNegative && time.Since(negAt) < negativeCacheTTL {
		metrics.NegativeCacheHits.Inc()
		return domain.EndpointData{}, sql.ErrNoRows
	}

	val, err, _ := r.sfGroup.Do(path, func() (interface{}, error) {
		// Re-check under singleflight in case another goroutine already
		// resolved this while we were waiting for the lock/Do call.
		r.mu.RLock()
		data, exists := r.endpoints[path]
		r.mu.RUnlock()
		if exists {
			return data, nil
		}
		metrics.DBQueriesTotal.Inc()
		dbData, err := r.db.GetEndpointByPath(path)
		if err != nil {
			if err == sql.ErrNoRows {
				r.markNegative(path)
			}
			return domain.EndpointData{}, err
		}

		r.UpdateEndpoint(dbData)
		return dbData, nil
	})

	if err != nil {
		return domain.EndpointData{}, err
	}

	return val.(domain.EndpointData), nil
}

func (r *Registry) markNegative(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.negativeCache[path] = time.Now()
}

func (r *Registry) UpdateEndpoint(data domain.EndpointData) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints[data.Path] = data
	// If this path was previously negatively cached (e.g. it was just added
	// via a migration or the admin API), clear the stale negative entry so
	// it doesn't shadow the now-valid endpoint until the TTL expires.
	delete(r.negativeCache, data.Path)
}

func (r *Registry) HandlePubSubUpdates(updates <-chan domain.EndpointData) {
	for update := range updates {
		r.UpdateEndpoint(update)
	}
}
