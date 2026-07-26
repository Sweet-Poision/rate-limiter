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


type tokenData struct {
	refil_wait_time_ms int
	max_limit_bucket int
}

type key struct {
	userID int
	endpoint string
}

type value struct {
	tokens chan struct {}
	endpoint string
}

var  (
	timeMap map[string] tokenData
	timeMapMu sync.RWMutex
	mp map[key] *value
	mu sync.RWMutex
	started bool = false	
)


func allow(userId int, endpoint string) bool {
	if(!started) {
		return false;
	}
	userKey := key{userId, endpoint}
	mu.RLock()
	var data, ok = mp[userKey]
	mu.RUnlock()
	
	if(!ok) {
		mu.Lock()
		if data, ok = mp[userKey]; !ok {
			timeMapMu.RLock()
			x, epOk := timeMap[endpoint]
			timeMapMu.RUnlock()
			if !epOk {
				mu.Unlock()
				return false
			}
			limit := x.max_limit_bucket
			
			data = &value{make(chan struct{}, limit), endpoint}
			for i:=0; i < limit; i++ {
				data.tokens <- struct{}{}
			}
			mp[userKey] = data
			go refill(userKey)
		}
		mu.Unlock()
		
	}
	select {
		case <- data.tokens:
			return true
		default:
			return false
	}
	
}

func refill(userKey key) {
	// refilling locks the map, if the map size is too big, then it will cause issue for new user creation. Thus we need to have multiple maps sharded
	for {
		
		mu.RLock()
		val, ok := mp[userKey]
		mu.RUnlock()

		if !ok {
			return
		}
		
		timeMapMu.RLock()
		sleepTimeMS := timeMap[val.endpoint].refil_wait_time_ms
		timeMapMu.RUnlock()
	
		time.Sleep(time.Millisecond * time.Duration(sleepTimeMS))
	
		select {
			case val.tokens <- struct{}{}:
			default:
		}
	}
}


func initTimeMap() {
	timeMapMu.Lock()
	timeMap["api/v1/health1"] = tokenData{100, 5}
	timeMap["api/v1/health2"] = tokenData{200, 10}
	timeMap["api/v1/health3"] = tokenData{250, 12}
	timeMap["api/v1/health4"] = tokenData{400, 25}
	timeMapMu.Unlock()
}

func rateLimiter() {
	mp = make(map[key] * value)
	timeMap = make(map[string] tokenData)
	initTimeMap()

	started = true
}