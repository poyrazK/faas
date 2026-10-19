package api

import "time"

type HostingTrafficCounters struct {
	Requests     int64 `json:"requests"`
	PayloadBytes int64 `json:"payload_bytes"`
	CacheHits    int64 `json:"cache_hits"`
	CacheMisses  int64 `json:"cache_misses"`
	ClientErrors int64 `json:"client_errors"`
	ServerErrors int64 `json:"server_errors"`
}
type HostingTrafficBucket struct {
	Start time.Time `json:"start"`
	HostingTrafficCounters
}
type HostingTrafficFailure struct {
	Start    time.Time `json:"start"`
	Status   int       `json:"status"`
	Code     string    `json:"code"`
	Requests int64     `json:"requests"`
}

// Weighted Analytics Engine observations are approximate, not billing or health evidence.
type HostingTraffic struct {
	SiteID            string    `json:"site_id"`
	Source            string    `json:"source"`
	Approximate       bool      `json:"approximate"`
	PeriodStart       time.Time `json:"period_start"`
	PeriodEnd         time.Time `json:"period_end"`
	ObservedAt        time.Time `json:"observed_at"`
	MaxSampleInterval int64     `json:"max_sample_interval"`
	HostingTrafficCounters
	Buckets  []HostingTrafficBucket  `json:"buckets"`
	Failures []HostingTrafficFailure `json:"failures"`
}
