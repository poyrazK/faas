package operatorobs

import (
	"encoding/json"
	"time"
)

const RuntimeBuildRecoveryScope = "replica_observations_of_shared_fleet"

// All aggregates count apid replica views, never unique builds or obligations.
type RuntimeBuildRecoveryCounts struct {
	Expected                int `json:"expected"`
	Observed                int `json:"observed"`
	Complete                int `json:"complete"`
	Missing                 int `json:"missing"`
	Stale                   int `json:"stale"`
	Conflicting             int `json:"conflicting"`
	Unavailable             int `json:"unavailable"`
	Unexpected              int `json:"unexpected"`
	NonemptyViews           int `json:"nonempty_views"`
	EmptyViews              int `json:"empty_views"`
	RunningViews            int `json:"running_views"`
	CleanupViews            int `json:"cleanup_views"`
	RetriedCleanupViews     int `json:"retried_cleanup_views"`
	UnsuccessfulCollections int `json:"unsuccessful_collections"`
}

type RuntimeBuildRecoverySummary struct {
	Scope      string                     `json:"scope"`
	State      string                     `json:"state"`
	Reason     string                     `json:"reason,omitempty"`
	Counts     RuntimeBuildRecoveryCounts `json:"counts"`
	Incomplete bool                       `json:"incomplete"`
}

type RuntimeBuildRecoveryHealth struct {
	RuntimeBuildRecoverySummary
	Items        []RuntimeBuildRecoveryItem `json:"items"`
	OmittedItems int                        `json:"omitted_items"`
}

type RuntimeBuildRecoveryRunning struct {
	RuntimeHandoffSignal
	Values *RuntimeBuildRecoveryRunningValues `json:"values,omitempty"`
}

type RuntimeBuildRecoveryRunningValues struct {
	Count            int64      `json:"count"`
	OldestStartedAt  *time.Time `json:"oldest_started_at,omitempty"`
	OldestAgeSeconds *float64   `json:"oldest_age_seconds,omitempty"`
}

type RuntimeBuildRecoveryCleanup struct {
	RuntimeHandoffSignal
	Values *RuntimeBuildRecoveryCleanupValues `json:"values,omitempty"`
}

type RuntimeBuildRecoveryCleanupValues struct {
	Pending          int64      `json:"pending"`
	Retried          int64      `json:"retried"`
	OldestCreatedAt  *time.Time `json:"oldest_created_at,omitempty"`
	OldestAgeSeconds *float64   `json:"oldest_age_seconds,omitempty"`
}

type RuntimeBuildRecoveryItem struct {
	Job              string                      `json:"job"`
	Node             string                      `json:"node,omitempty"`
	TargetID         string                      `json:"target_id,omitempty"`
	Unexpected       bool                        `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                  `json:"scrape_observed_at,omitempty"`
	State            string                      `json:"state"`
	Collection       RuntimeHandoffCollection    `json:"collection"`
	Running          RuntimeBuildRecoveryRunning `json:"running"`
	Cleanup          RuntimeBuildRecoveryCleanup `json:"cleanup"`
}

func runtimeBuildRecoverySelected(role string) bool {
	return role == "single-box-v2" || role == "control-plane-v2"
}

func CloneRuntimeBuildRecoveryHealth(in *RuntimeBuildRecoveryHealth) *RuntimeBuildRecoveryHealth {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeBuildRecoveryHealth
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}
