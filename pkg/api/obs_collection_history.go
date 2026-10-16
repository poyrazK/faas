package api

import "time"

// ObsAlertCollectionCoverage describes retained cadence evidence, not uptime.
type ObsAlertCollectionCoverage struct {
	Since                             time.Time `json:"since"`
	AsOf                              time.Time `json:"as_of"`
	Scope                             string    `json:"scope"`
	State                             string    `json:"state"`
	MaxAgeSeconds                     int64     `json:"max_age_seconds"`
	RetentionSeconds                  int64     `json:"retention_seconds"`
	WindowMicroseconds                int64     `json:"window_microseconds"`
	SuccessfulObservationMicroseconds int64     `json:"successful_observation_microseconds"`
	FailedObservationMicroseconds     int64     `json:"failed_observation_microseconds"`
	UnknownMicroseconds               int64     `json:"unknown_microseconds"`
	Successes                         int64     `json:"successes"`
	Failures                          int64     `json:"failures"`
	Limitations                       []string  `json:"limitations"`
}

type ObsAlertCollectionHistoryInterval struct {
	From   time.Time `json:"from"`
	Until  time.Time `json:"until"`
	State  string    `json:"state"`
	Reason string    `json:"reason"`
}

type ObsAlertCollectionHistory struct {
	Coverage       ObsAlertCollectionCoverage          `json:"coverage"`
	Collection     ObsAlertCollectionHealth            `json:"collection"`
	Items          []ObsAlertCollectionHistoryInterval `json:"items"`
	TotalIntervals int                                 `json:"total_intervals"`
	Limit          int                                 `json:"limit"`
	Truncated      bool                                `json:"truncated"`
}
