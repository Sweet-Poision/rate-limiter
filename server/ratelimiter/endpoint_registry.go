package ratelimiter

import "sync"

type endpointData struct {
	refilWaitTimeMS int
	maxLimitBucket  int
}

type safeEndpointRegistry struct {
	mu        sync.RWMutex
	endpoints map[string]endpointData
}

func (r *safeEndpointRegistry) get(endpoint string) (endpointData, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	data, ok := r.endpoints[endpoint]
	return data, ok
}
