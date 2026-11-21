package api

import "time"

// ObsRuntimeProcess compares sampled uptime at two pinned evaluation times.
// Changed means an observed process replacement, not a proven crash or a
// complete restart count. Counts cover all inspected targets before previewing.
type ObsRuntimeProcess struct {
	State        string                  `json:"state"`
	Reason       string                  `json:"reason,omitempty"`
	ComparisonAt *time.Time              `json:"comparison_at,omitempty"`
	Counts       ObsRuntimeProcessCounts `json:"counts"`
	Incomplete   bool                    `json:"incomplete"`
}

type ObsRuntimeProcessCounts struct {
	Fresh                    int `json:"fresh"`
	Missing                  int `json:"missing"`
	Stale                    int `json:"stale"`
	Conflicting              int `json:"conflicting"`
	Unavailable              int `json:"unavailable"`
	Compared                 int `json:"compared"`
	Changed                  int `json:"changed"`
	Unchanged                int `json:"unchanged"`
	SameRevisionChanges      int `json:"same_revision_changes"`
	DifferentRevisionChanges int `json:"different_revision_changes"`
	UnknownRevisionChanges   int `json:"unknown_revision_changes"`
	NewTargets               int `json:"new_targets"`
	RemovedTargets           int `json:"removed_targets"`
	RepeatedSamples          int `json:"repeated_samples"`
	ComparisonUnavailable    int `json:"comparison_unavailable"`
}

type ObsRuntimeProcessItem struct {
	State                 string     `json:"state"`
	Reason                string     `json:"reason,omitempty"`
	UptimeSeconds         *float64   `json:"uptime_seconds,omitempty"`
	ObservedAt            *time.Time `json:"observed_at,omitempty"`
	Comparison            string     `json:"comparison,omitempty"`
	ComparisonReason      string     `json:"comparison_reason,omitempty"`
	PreviousUptimeSeconds *float64   `json:"previous_uptime_seconds,omitempty"`
	PreviousObservedAt    *time.Time `json:"previous_observed_at,omitempty"`
	RevisionRelation      string     `json:"revision_relation,omitempty"`
}
