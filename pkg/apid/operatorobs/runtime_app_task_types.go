package operatorobs

import (
	"encoding/json"
	"strings"
	"time"
)

// Aggregates count replica views of a shared fleet, never unique tasks.
type RuntimeAppTaskCounts struct {
	SaturatedViews          int `json:"saturated_views"`
	OccupiedViews           int `json:"occupied_views"`
	RunnerStoppedViews      int `json:"runner_stopped_views"`
	RunnerRunningViews      int `json:"runner_running_views"`
	DispatchDisabledViews   int `json:"dispatch_disabled_views"`
	DispatchEnabledViews    int `json:"dispatch_enabled_views"`
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

type RuntimeAppTaskClassCounts struct {
	Kind            string `json:"kind"`
	Class           string `json:"class"`
	NonemptyViews   int    `json:"nonempty_views"`
	EmptyViews      int    `json:"empty_views"`
	UnknownAgeViews int    `json:"unknown_age_views"`
}

const RuntimeAppTaskScope = "replica_observations_of_shared_fleet"

type RuntimeAppTaskSummary struct {
	Scope      string                      `json:"scope"`
	State      string                      `json:"state"`
	Reason     string                      `json:"reason,omitempty"`
	Counts     RuntimeAppTaskCounts        `json:"counts"`
	Classes    []RuntimeAppTaskClassCounts `json:"classes"`
	Incomplete bool                        `json:"incomplete"`
}

type RuntimeAppTaskHealth struct {
	RuntimeAppTaskSummary
	Items        []RuntimeAppTaskItem `json:"items"`
	OmittedItems int                  `json:"omitted_items"`
}

type RuntimeAppTaskObservation struct {
	RuntimeHandoffSignal
	Values *RuntimeAppTaskObservationValues `json:"values,omitempty"`
}

type RuntimeAppTaskObservationValues struct {
	Enabled bool `json:"enabled"`
}

type RuntimeAppTaskClass struct {
	Kind string `json:"kind"`
	RuntimeHandoffSignal
	Class  string                     `json:"class"`
	Values *RuntimeAppTaskClassValues `json:"values,omitempty"`
}

type RuntimeAppTaskClassValues struct {
	Pending               int64      `json:"pending"`
	UnknownAge            int64      `json:"unknown_age"`
	OldestBoundaryAt      *time.Time `json:"oldest_boundary_at,omitempty"`
	OldestKnownAgeSeconds *float64   `json:"oldest_known_age_seconds,omitempty"`
}

type RuntimeAppTaskItem struct {
	Job              string                    `json:"job"`
	Node             string                    `json:"node,omitempty"`
	TargetID         string                    `json:"target_id,omitempty"`
	Unexpected       bool                      `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                `json:"scrape_observed_at,omitempty"`
	State            string                    `json:"state"`
	Collection       RuntimeHandoffCollection  `json:"collection"`
	Dispatch         RuntimeAppTaskDispatch    `json:"dispatch"`
	Observation      RuntimeAppTaskObservation `json:"observation"`
	Classes          []RuntimeAppTaskClass     `json:"classes"`
}

func runtimeAppTaskClasses() []string {
	var out []string
	for _, kind := range []string{"release", "manual", "cron"} {
		for _, class := range []string{"queued", "retry_wait", "held", "deadline_overdue", "restoring", "running", "cancellation_pending", "lease_expired", "unknown"} {
			out = append(out, kind+"/"+class)
		}
	}
	return out
}

func newRuntimeAppTaskSummary() RuntimeAppTaskSummary {
	h := RuntimeAppTaskSummary{Scope: RuntimeAppTaskScope}
	for _, class := range runtimeAppTaskClasses() {
		h.Classes = append(h.Classes, runtimeAppTaskClassCounts(class))
	}
	return h
}

func CloneRuntimeAppTaskHealth(in *RuntimeAppTaskHealth) *RuntimeAppTaskHealth {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeAppTaskHealth
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func CloneRuntimeAppTaskSummary(in *RuntimeAppTaskSummary) *RuntimeAppTaskSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Classes = append([]RuntimeAppTaskClassCounts{}, in.Classes...)
	return &out
}

func runtimeAppTaskJobs(role string) []string {
	if role == "single-box-v2" {
		return []string{"schedd"}
	}
	if role == "control-plane-v2" {
		return []string{"schedd", "schedd-compute"}
	}
	return nil
}

func runtimeAppTaskClassCounts(key string) RuntimeAppTaskClassCounts {
	kind, class, _ := strings.Cut(key, "/")
	return RuntimeAppTaskClassCounts{Kind: kind, Class: class}
}
func runtimeAppTaskUnknownAgeClass(class string) bool {
	return class == "unknown" || class == "restoring" || class == "retry_wait" || class == "held"
}

type RuntimeAppTaskDispatch struct {
	RuntimeHandoffSignal
	Values *RuntimeAppTaskDispatchValues `json:"values,omitempty"`
}
type RuntimeAppTaskDispatchValues struct {
	Enabled            bool       `json:"enabled"`
	Running            bool       `json:"running"`
	SlotsConfigured    int64      `json:"slots_configured"`
	SlotsInUse         int64      `json:"slots_in_use"`
	LastClaimSuccessAt *time.Time `json:"last_claim_success_at,omitempty"`
	LastSweepSuccessAt *time.Time `json:"last_sweep_success_at,omitempty"`
}
