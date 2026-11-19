package operatorobs

import (
	"encoding/json"
	"time"
)

// Aggregates count replica views of a shared fleet, never unique deployments.
type RuntimePostCaptureCounts struct {
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
	UnknownAgeViews         int `json:"unknown_age_views"`
	UnsuccessfulCollections int `json:"unsuccessful_collections"`
	EnabledViews            int `json:"enabled_views"`
	DisabledViews           int `json:"disabled_views"`
}

type RuntimePostCaptureClassCounts struct {
	Class           string `json:"class"`
	NonemptyViews   int    `json:"nonempty_views"`
	EmptyViews      int    `json:"empty_views"`
	UnknownAgeViews int    `json:"unknown_age_views"`
}

const RuntimePostCaptureScope = "replica_observations_of_shared_fleet"

type RuntimePostCaptureSummary struct {
	Scope      string                          `json:"scope"`
	State      string                          `json:"state"`
	Reason     string                          `json:"reason,omitempty"`
	Counts     RuntimePostCaptureCounts        `json:"counts"`
	Classes    []RuntimePostCaptureClassCounts `json:"classes"`
	Incomplete bool                            `json:"incomplete"`
}

type RuntimePostCaptureHealth struct {
	RuntimePostCaptureSummary
	Items        []RuntimePostCaptureItem `json:"items"`
	OmittedItems int                      `json:"omitted_items"`
}

type RuntimePostCaptureObservation struct {
	RuntimeHandoffSignal
	Values *RuntimePostCaptureObservationValues `json:"values,omitempty"`
}

type RuntimePostCaptureObservationValues struct {
	Enabled bool `json:"enabled"`
}

type RuntimePostCaptureClass struct {
	RuntimeHandoffSignal
	Class  string                         `json:"class"`
	Values *RuntimePostCaptureClassValues `json:"values,omitempty"`
}

type RuntimePostCaptureClassValues struct {
	Pending               int64      `json:"pending"`
	UnknownAge            int64      `json:"unknown_age"`
	OldestBoundaryAt      *time.Time `json:"oldest_boundary_at,omitempty"`
	OldestKnownAgeSeconds *float64   `json:"oldest_known_age_seconds,omitempty"`
}

type RuntimePostCaptureItem struct {
	Job              string                        `json:"job"`
	Node             string                        `json:"node,omitempty"`
	TargetID         string                        `json:"target_id,omitempty"`
	Unexpected       bool                          `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                    `json:"scrape_observed_at,omitempty"`
	State            string                        `json:"state"`
	Collection       RuntimeHandoffCollection      `json:"collection"`
	Observation      RuntimePostCaptureObservation `json:"observation"`
	Classes          []RuntimePostCaptureClass     `json:"classes"`
}

func runtimePostCaptureClasses() []string {
	return []string{"publication_pending", "activation_pending", "dependency_held", "compatibility_held", "hosting_verifying", "hosting_retry", "gate_overdue", "unknown"}
}

func newRuntimePostCaptureSummary() RuntimePostCaptureSummary {
	h := RuntimePostCaptureSummary{Scope: RuntimePostCaptureScope}
	for _, class := range runtimePostCaptureClasses() {
		h.Classes = append(h.Classes, RuntimePostCaptureClassCounts{Class: class})
	}
	return h
}

func CloneRuntimePostCaptureHealth(in *RuntimePostCaptureHealth) *RuntimePostCaptureHealth {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimePostCaptureHealth
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func CloneRuntimePostCaptureSummary(in *RuntimePostCaptureSummary) *RuntimePostCaptureSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Classes = append([]RuntimePostCaptureClassCounts{}, in.Classes...)
	return &out
}

func runtimePostCaptureJobs(role string) []string {
	if role == "single-box-v2" || role == "control-plane-v2" {
		return []string{"imaged"}
	}
	return nil
}
