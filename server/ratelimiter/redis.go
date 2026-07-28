package ratelimiter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

type EndpointUpdateMessage struct {
	Path             string `json:"path"`
	RefillWaitTimeMS int    `json:"refil_wait_time_ms"`
	MaxLimit         int    `json:"max_limit"`
}

var rdb *redis.Client
var ctx context.Context

var rateLimitScript = redis.NewScript(`
local key = KEYS[1]
local refill_rate_ms = tonumber(ARGV[1])
local max_limit = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

local data = redis.call("HMGET", key, "tokens", "last_updated")
local tokens = tonumber(data[1])
local last_updated = tonumber(data[2])

if tokens == nil then
    tokens = max_limit
    last_updated = now
else
    local elapsed = now - last_updated
    if elapsed > 0 then
        local added = elapsed * (max_limit / refill_rate_ms)
        tokens = math.min(max_limit, tokens + added)
        last_updated = now
    end
end

if tokens >= 1 then
    tokens = tokens - 1
    redis.call("HMSET", key, "tokens", tokens, "last_updated", last_updated)
    redis.call("PEXPIRE", key, refill_rate_ms * 2)
    return 1
else
    return 0
end
`)

func InitRedis() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		log.Fatal("REDIS_ADDR environment variable is not set.")
	}

	rdb = redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0,
		Protocol: 2,
	})

	ctx = context.Background()
}

func EvaluateRateLimit(userID string, path string, data endpointData) (bool, error) {
	redisKey := fmt.Sprintf("rate_limit:%s:%s", userID, path)
	now := time.Now().UnixMilli()

	keys := []string{redisKey}
	args := []any{
		data.refilWaitTimeMS,
		data.maxLimitBucket,
		now,
	}

	result, err := rateLimitScript.Run(ctx, rdb, keys, args...).Int()
	if err != nil {
		return false, err
	}

	return result == 1, nil
}

func ListenForEndpointUpdate() {
	pubsub := rdb.Subscribe(ctx, "endpoint_updates")

	go func() {
		ch := pubsub.Channel()
		for msg := range ch {
			var update EndpointUpdateMessage
			err := json.Unmarshal([]byte(msg.Payload), &update)
			if err != nil {
				log.Printf("Failed to parse pubsub message: %v", err)
				continue
			}

			// Atomically update local shadow/live map
			availableEndpoints.mu.Lock()
			availableEndpoints.endpoints[update.Path] = endpointData{
				refilWaitTimeMS: update.RefillWaitTimeMS,
				maxLimitBucket:  update.MaxLimit,
			}
			availableEndpoints.mu.Unlock()

			log.Printf("Cache synchronized via Pub/Sub for path: %s", update.Path)
		}
	}()
}
