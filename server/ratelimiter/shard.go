package ratelimiter

import "sync"

type Node struct {
	userKey key
	bucket  tokenBucket
	prev    *Node
	next    *Node
}

type shard struct {
	mu       sync.RWMutex
	bucket   map[key]*Node
	head     *Node
	tail     *Node
	size     int
	capacity int
}

type shardedStorage struct {
	shards     []shard
	shardCount uint32
}

func NewShardedStorage(count uint32, capacity int) shardedStorage {
	shards := make([]shard, count)
	for i := range shards {
		shards[i] = shard{
			bucket:   make(map[key]*Node),
			capacity: capacity,
		}
	}
	return shardedStorage{
		shards:     shards,
		shardCount: count,
	}
}

func (s *shard) pushHead(n *Node) {
	n.next = s.head
	n.prev = nil
	if s.head != nil {
		s.head.prev = n
	}
	s.head = n
	if s.tail == nil {
		s.tail = n
	}
	s.size++
}

func (s *shard) remove(n *Node) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		s.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		s.tail = n.prev
	}
	s.size--
}

func (s *shard) moveToHead(n *Node) {
	if s.head == n {
		return
	}
	s.remove(n)
	s.pushHead(n)
}

func (s *shard) popTail() key {
	tail := s.tail
	if tail == nil {
		return key{}
	}
	s.remove(tail)
	return tail.userKey
}
