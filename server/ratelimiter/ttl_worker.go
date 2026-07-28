package ratelimiter

import "time"

const TTL_MS = 300000

func StartTTLWorker(interval time.Duration, ttlMs int64) {
	go func() {
		ticker := time.NewTicker(interval)
		for range ticker.C {
			currentTime := time.Now().UnixMilli()

			for i := 0; i < len(shardMaps.shards); i++ {
				targetShard := &shardMaps.shards[i]

				targetShard.mu.Lock()

				for targetShard.tail != nil {
					if currentTime-targetShard.tail.bucket.lastTouchTime > ttlMs {
						evictedKey := targetShard.popTail()
						delete(targetShard.bucket, evictedKey)
					} else {
						break
					}
				}
				targetShard.mu.Unlock()
			}
		}
	}()
}
