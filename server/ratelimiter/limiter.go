package ratelimiter

import (
	"time"
	"database/sql"
	"golang.org/x/sync/singleflight"
)

const (
	SHARD_SIZE = 256
	CAPACITY   = 1000
)

type key struct {
	userID     int
	endpointId string
}

var (
	started            bool
	availableEndpoints safeEndpointRegistry
	shardMaps          shardedStorage
	sfGroup				singleflight.Group
)

func GetEndpoint(path string) (endpointData, error) {
	availableEndpoints.mu.RLock()
	data, exists := availableEndpoints.endpoints[path]
	availableEndpoints.mu.RUnlock()

	if exists {
		return data, nil
	}

	val, err, _ := sfGroup.Do(path, func() (interface{}, error) {
		availableEndpoints.mu.RLock()
		data, exists := availableEndpoints.endpoints[path]
		availableEndpoints.mu.RUnlock()

		if(exists) {
			return data, nil
		}

		var d endpointData
		query := "SELECT refil_wait_time_ms, max_limit FROM endpoints WHERE path = $1"
		err := db.QueryRow(query, path).Scan(&d.refilWaitTimeMS, &d.maxLimitBucket)
		if err != nil {
			if err == sql.ErrNoRows {
				return endpointData{}, err
			}
			return endpointData{}, err
		}

		availableEndpoints.mu.Lock()
		availableEndpoints.endpoints[path] = d
		availableEndpoints.mu.Unlock()

		return d, nil
	})

	if err != nil {
		return endpointData{}, err
	}

	return val.(endpointData), nil
}

func Allow(userId int, endpoint string) bool {
	if !started {
		return false
	}

	epMetadata, epOk := availableEndpoints.get(endpoint)
	if !epOk {
		return false
	}

	userKey := key{userId, endpoint}
	shardIndex := getShardIndex(hashKey(userId, endpoint), shardMaps.shardCount)
	targetShard := &shardMaps.shards[shardIndex]

	currentTime := time.Now().UnixMilli()

	targetShard.mu.Lock()
	defer targetShard.mu.Unlock()

	node, exists := targetShard.bucket[userKey]
	if !exists {
		if targetShard.size >= targetShard.capacity {
			evictedKey := targetShard.popTail()
			delete(targetShard.bucket, evictedKey)
		}

		newNode := &Node{
			userKey: userKey,
			bucket: tokenBucket{
				currentTokens: float64(epMetadata.maxLimitBucket - 1),
				lastTouchTime: currentTime,
			},
		}
		targetShard.pushHead(newNode)
		targetShard.bucket[userKey] = newNode
		return true
	}

	if node.bucket.tryConsume(currentTime, epMetadata) {
		targetShard.moveToHead(node)
		return true
	}

	return false
}

func RateLimiter() {
	started = false
	// shardMaps = NewShardedStorage(SHARD_SIZE, CAPACITY)

	availableEndpoints = safeEndpointRegistry{
		endpoints: make(map[string]endpointData),
	}

	// availableEndpoints.endpoints["api/v1/health1"] = endpointData{refilWaitTimeMS: 100, maxLimitBucket: 5}
	// availableEndpoints.endpoints["api/v1/health2"] = endpointData{refilWaitTimeMS: 200, maxLimitBucket: 10}

	// StartTTLWorker(time.Minute*1, TTL_MS)
	LoadEndpointsFromDB()

	started = true
}
