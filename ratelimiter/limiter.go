// As soon as the service boots up, the service first handles the spike or traffic and then starts to limit the calls.
// Rate Limiter will have a TTL (TODO), the request will go in the queue for a time, if it gets served before that then all fine else, it will expire
// When the bucket is already full then reject the request

// we are dealing only one person, then the server starts and i am lets say already started to make many requests then initially all the token should be available and ready to take calls as I think, but as i think we dont go for initial burst the sytem will have less load but then the whole point of rate limiting is not there that it is supposed to handle the max condition under some limit,

// as part of the rejected called, i think there should be some retry algorithm in the clients code and the server should completely drop the packet, i dont understand will that be possible that we queue in the request as per token and then keep having requests and then pop it as the other server we are routing from the rate limiter is able to handle it? but then there will be case when some user might just go away in case of how much time is it going to take staying in the queue, we can have a time limit that is some research based time after which we should drop the connection and let the client retry, so it goes into queue wait for the rate limiter to send it to the actual server and also have some ttl,

// i dont know about refill i think it will be blocked mostly as i told in my prev statement but it will be in queue...

package ratelimiter

import (
	"sync"
	"time"
)

const ALLOWED_PER_SECOND int = 10
const MAX_LIMIT_BUCKET int = 5
const REFILL_WAIT_TIME_MS int = 1000 / ALLOWED_PER_SECOND   // miliseconds

var (
	mp 		map[int] chan struct{}
	mu 		sync.RWMutex
	started bool = false
)	// We as of now are using single map

func allow(userId int) bool {
	if(!started) {
		return false;
	}
	mu.RLock()
	var data, ok = mp[userId]
	mu.RUnlock()
	
	if(!ok) {
		mu.Lock()
		if data, ok = mp[userId]; !ok {
			data = make(chan struct{}, MAX_LIMIT_BUCKET)
			for i:=0; i < MAX_LIMIT_BUCKET; i++ {
				data <- struct{}{}
			}
			mp[userId] = data
		}
		mu.Unlock()
	}
	select {
		case <- data:
			return true
		default:
			return false
	}
	
}

func refill() {
	// refilling locks the map, if the map size is too big, then it will cause issue for new user creation. Thus we need to have multiple maps sharded
	mu.RLock()
	for _, val := range mp {
		select {
			case val <- struct{}{}:
			default:
		}
	}
	mu.RUnlock()
}


func rateLimiter() {
	mp = make(map[int]chan struct{})
	started = true
	go func() {
		for {
			time.Sleep(time.Millisecond * time.Duration(REFILL_WAIT_TIME_MS))
			refill()
		}
	}()

}