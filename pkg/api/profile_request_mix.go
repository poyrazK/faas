package api

import "time"

// Frozen, bounded summaries of observed telemetry, never raw requests.
type ProfileRequestMixSnapshot struct {
	CapturedAt       time.Time                `json:"captured_at"`
	Status           string                   `json:"status"`
	Complete         bool                     `json:"complete"`
	Reason           string                   `json:"reason"`
	Baseline         *ProfileRequestMixWindow `json:"baseline,omitempty"`
	Candidate        *ProfileRequestMixWindow `json:"candidate,omitempty"`
	Warnings         []string                 `json:"warnings"`
	RouteDifference  *float64                 `json:"route_difference,omitempty"`
	StatusDifference *float64                 `json:"status_difference,omitempty"`
}

type ProfileRequestMixWindow struct {
	Query     ProfileQuery             `json:"query"`
	Total     int64                    `json:"total"`
	Routes    []ProfileRequestMixGroup `json:"routes"`
	Statuses  []ProfileRequestMixGroup `json:"statuses"`
	Truncated bool                     `json:"truncated"`
}

type ProfileRequestMixGroup struct {
	Label    string `json:"label"`
	Method   string `json:"method"`
	Requests int64  `json:"requests"`
}
