package limiter

import (
	"database/sql"
	"sync"

	"ratelimiter/internal/domain"
	"ratelimiter/internal/repository"

	"golang.org/x/sync/singleflight"
)

type Registry struct {
	mu        sync.RWMutex
	endpoints map[string]domain.EndpointData
	db        *repository.PostgresStore
	sfGroup   singleflight.Group
}

func NewRegistry(db *repository.PostgresStore, initialData map[string]domain.EndpointData) *Registry {
	return &Registry{
		endpoints: initialData,
		db:        db,
	}
}

func (r *Registry) GetEndpoint(path string) (domain.EndpointData, error) {
	r.mu.RLock()
	data, exists := r.endpoints[path]
	r.mu.RUnlock()

	if exists {
		return data, nil
	}

	val, err, _ := r.sfGroup.Do(path, func() (interface{}, error) {
		r.mu.RLock()
		data, exists := r.endpoints[path]
		r.mu.RUnlock()

		if exists {
			return data, nil
		}

		dbData, err := r.db.GetEndpointByPath(path)
		if err != nil {
			return domain.EndpointData{}, err
		}

		r.UpdateEndpoint(dbData)
		return dbData, nil
	})

	if err != nil {
		if err == sql.ErrNoRows {
			return domain.EndpointData{}, err
		}
		return domain.EndpointData{}, err
	}

	return val.(domain.EndpointData), nil
}

func (r *Registry) UpdateEndpoint(data domain.EndpointData) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints[data.Path] = data
}

func (r *Registry) HandlePubSubUpdates(updates <-chan domain.EndpointData) {
	for update := range updates {
		r.UpdateEndpoint(update)
	}
}
