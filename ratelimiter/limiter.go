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
	// Contains the expiry time and max bucket amount

	refilWaitTimeMS int
	maxLimitBucket  int
}

type payload struct {
	// Contains the information of endpoint and their token data

	epName    string
	tokenData tokenData
}

type key struct {
	// the key for the map where all the data is stored which we use for limiting

	userID     int
	endpointId int
}

var (
	timeMap   map[int]tokenData
	timeMapMu sync.RWMutex

	mp map[key]chan struct{}
	mu sync.RWMutex

	endpoints map[string]int
	epMu      sync.RWMutex

	started bool = false
)

func allow(userId int, endpoint string) bool {
	if !started {
		return false
	}
	epMu.RLock()
	// check if id exists

	epId, epExists := endpoints[endpoint]
	epMu.RUnlock()

	if !epExists {
		return false
	}

	userKey := key{userId, epId}
	mu.RLock()
	data, ok := mp[userKey]
	mu.RUnlock()

	if !ok {
		mu.Lock()
		if data, ok = mp[userKey]; !ok {
			timeMapMu.RLock()
			x, epOk := timeMap[epId]
			timeMapMu.RUnlock()
			if !epOk {
				mu.Unlock()
				return false
			}
			limit := x.maxLimitBucket

			data = make(chan struct{}, limit)
			for i := 0; i < limit; i++ {
				data <- struct{}{}
			}
			mp[userKey] = data
			go refill(userKey)
		}
		mu.Unlock()

	}
	select {
	case <-data:
		return true
	default:
		return false
	}

}

func refill(userKey key) {
	// refilling locks the map, if the map size is too big, then it will cause issue for new user creation. Thus we need to have multiple maps sharded
	for {

		mu.RLock()
		val, ok := mp[userKey] // val is the chan of structs
		mu.RUnlock()

		if !ok {
			return
		}

		// check if endpoint exists but already the allow function has checked it

		timeMapMu.RLock()
		sleepTimeMS := timeMap[userKey.endpointId].refilWaitTimeMS
		timeMapMu.RUnlock()

		time.Sleep(time.Millisecond * time.Duration(sleepTimeMS))

		select {
		case val <- struct{}{}:
		default:
		}
	}
}

func initMaps(eps []payload) {
	endpoints = make(map[string]int, len(eps))
	timeMap = make(map[int]tokenData, len(eps))

	for i, ep := range eps {
		endpoints[ep.epName] = i
		timeMap[i] = ep.tokenData
	}
}

func rateLimiter() {
	mp = make(map[key]chan struct{})
	listOfEndpoints := []payload{
		payload{
			epName: "api/v1/health1",
			tokenData: tokenData{
				refilWaitTimeMS: 100,
				maxLimitBucket:  5,
			},
		},
		payload{
			epName: "api/v1/health2",
			tokenData: tokenData{
				refilWaitTimeMS: 200,
				maxLimitBucket:  10,
			},
		},
		payload{
			epName: "api/v1/health3",
			tokenData: tokenData{
				refilWaitTimeMS: 250,
				maxLimitBucket:  12,
			},
		},
		payload{
			epName: "api/v1/health4",
			tokenData: tokenData{
				refilWaitTimeMS: 400,
				maxLimitBucket:  25,
			},
		},
	}

	initMaps(listOfEndpoints)
	started = true
}
