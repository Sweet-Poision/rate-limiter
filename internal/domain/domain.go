package domain

type EndpointData struct {
	Path             string `json:"path"`
	RefillWaitTimeMS int    `json:"refill_wait_time_ms"`
	MaxLimit         int    `json:"max_limit"`
}
