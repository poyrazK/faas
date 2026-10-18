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
	FailureDetails *ObsAlertCollectionFailureDetails   `json:"failure_details,omitempty"`
	Coverage       ObsAlertCollectionCoverage          `json:"coverage"`
	Collection     ObsAlertCollectionHealth            `json:"collection"`
	Items          []ObsAlertCollectionHistoryInterval `json:"items"`
	TotalIntervals int                                 `json:"total_intervals"`
	Limit          int                                 `json:"limit"`
	Truncated      bool                                `json:"truncated"`
}

// FailureDetails is opt-in. It describes attempts, separately from coalesced
// cadence intervals; missing historical diagnostics never means healthy rules.
type ObsAlertCollectionFailureDetails struct {
	Items         []ObsAlertCollectionFailure `json:"items"`
	TotalFailures int64                       `json:"total_failures"`
	Limit         int                         `json:"limit"`
	Truncated     bool                        `json:"truncated"`
	Limitations   []string                    `json:"limitations"`
}

type ObsAlertCollectionFailure struct {
	AttemptedAt      time.Time                    `json:"attempted_at"`
	Reason           string                       `json:"reason"`
	DiagnosticsState string                       `json:"diagnostics_state"`
	Diagnostics      *ObsAlertCollectionRejection `json:"diagnostics"`
}

// Rejection contains observations from the exact rejected source snapshot,
// including complete counts and a bounded unhealthy-first metadata preview.
// It does not assert that every issue triggered the collector's first rejection.
type ObsAlertCollectionRejection struct {
	Scope      string              `json:"scope"`
	Reason     string              `json:"reason"`
	Totals     ObsRuleHealthTotals `json:"totals"`
	Items      []ObsRuleHealthItem `json:"items"`
	TotalItems int                 `json:"total_items"`
	Limit      int                 `json:"limit"`
	Truncated  bool                `json:"truncated"`
}
