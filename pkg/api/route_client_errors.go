package api

import "time"

// RouteHealthStatusCounts describes one watched response code, not all 4xx.
type RouteHealthStatusCounts struct {
	Requests  int64   `json:"requests"`
	Responses int64   `json:"responses"`
	Rate      float64 `json:"rate"`
}

type RouteHealthClientErrorWindow struct {
	Start     time.Time               `json:"start"`
	End       time.Time               `json:"end"`
	Candidate RouteHealthStatusCounts `json:"candidate"`
	Stable    RouteHealthStatusCounts `json:"stable"`
	Status    string                  `json:"status"`
	Reason    string                  `json:"reason"`
}

type RouteHealthClientErrorFinding struct {
	StatusCode int                            `json:"status_code"`
	Status     string                         `json:"status"`
	Reason     string                         `json:"reason"`
	Windows    []RouteHealthClientErrorWindow `json:"windows"`
}

// RouteHealthClientErrorReport is live advisory evidence. It does not alter
// aggregate route-health verdicts, canary decisions, recovery, or saved history.
type RouteHealthClientErrorReport struct {
	Status           string                          `json:"status"`
	Reason           string                          `json:"reason"`
	MinimumRequests  int64                           `json:"minimum_requests"`
	MinimumResponses int64                           `json:"minimum_responses"`
	RateFloor        float64                         `json:"rate_floor"`
	RateDelta        float64                         `json:"rate_delta"`
	RateFactor       float64                         `json:"rate_factor"`
	Statuses         []RouteHealthClientErrorFinding `json:"statuses"`
}
