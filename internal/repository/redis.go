package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"ratelimiter/internal/domain"
	"ratelimiter/internal/metrics"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
	script *redis.Script
}

// FIXED: previous formula was `elapsed * (max_limit / refill_rate_ms)`, which
// made the refill amount scale with max_limit and ignored elapsed time
// correctly — a 1000-capacity bucket refilled to full in ~1ms regardless of
// how much time had actually passed. Correct formula mirrors the original Go
// version: tokens added = elapsed_ms / refill_rate_ms (one token per
// refill_rate_ms of elapsed time), independent of max_limit.
const luaScript = `
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
        local added = elapsed / refill_rate_ms
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
`

func NewRedisStore(addr string) *RedisStore {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0,
		Protocol: 2,
	})

	return &RedisStore{
		client: client,
		script: redis.NewScript(luaScript),
	}
}

func (r *RedisStore) EvaluateRateLimit(ctx context.Context, userID string, data domain.EndpointData) (bool, error) {
	start := time.Now()
	defer func() {
		metrics.RedisEvalDuration.Observe(time.Since(start).Seconds())
	}()
	redisKey := fmt.Sprintf("rate_limit:%s:%s", userID, data.Path)
	now := time.Now().UnixMilli()

	keys := []string{redisKey}
	args := []any{
		data.RefillWaitTimeMS,
		data.MaxLimit,
		now,
	}

	result, err := r.script.Run(ctx, r.client, keys, args...).Int()
	if err != nil {
		return false, err
	}

	return result == 1, nil
}

func (r *RedisStore) ListenForUpdates(ctx context.Context, updateChan chan<- domain.EndpointData) {
	pubsub := r.client.Subscribe(ctx, "endpoint_updates")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for msg := range ch {
		var update domain.EndpointData
		err := json.Unmarshal([]byte(msg.Payload), &update)
		if err != nil {
			log.Printf("Failed to parse pubsub message: %v", err)
			continue
		}
		updateChan <- update
	}
}
