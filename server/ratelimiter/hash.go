package ratelimiter

const (
	OFFSET32 = 2166136261
	PRIME32  = 16777619
)

func getShardIndex(hash uint32, shardCount uint32) uint32 {
	return hash & (shardCount - 1)
}

func hashKey(id int, endpoint string) uint32 {
	var hash uint32 = OFFSET32
	for i := 0; i < 8; i++ {
		b := byte(id >> (i * 8))
		hash = hash ^ uint32(b)
		hash = hash * PRIME32
	}
	for i := 0; i < len(endpoint); i++ {
		hash = hash ^ uint32(endpoint[i])
		hash = hash * PRIME32
	}
	return hash
}
