package ratelimiter

import "math"

type tokenBucket struct {
	currentTokens float64
	lastTouchTime int64
}

// tryConsume replenishes tokens based on elapsed time since lastTouchTime,
// then attempts to consume one token. Returns true and mutates the bucket
// if a token was available, false otherwise (bucket left unchanged).
func (tb *tokenBucket) tryConsume(now int64, cfg endpointData) bool {
	deltaTime := now - tb.lastTouchTime
	tokenAddition := float64(deltaTime) / float64(cfg.refilWaitTimeMS)
	currentTotal := math.Min(tokenAddition+tb.currentTokens, float64(cfg.maxLimitBucket))

	if currentTotal >= 1 {
		tb.currentTokens = currentTotal - 1
		tb.lastTouchTime = now
		return true
	}

	return false
}
