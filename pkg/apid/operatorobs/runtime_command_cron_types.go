package operatorobs

import (
	"encoding/json"
	"time"
)

// Aggregates count scheduler ownership scopes, which may overlap; never unique schedules.
type RuntimeCommandCronCounts struct {
	DueScopes                 int `json:"due_scopes"`
	InactiveScopes            int `json:"inactive_scopes"`
	SweepRunningScopes        int `json:"sweep_running_scopes"`
	SweepIdleScopes           int `json:"sweep_idle_scopes"`
	SweepNeverStartedScopes   int `json:"sweep_never_started_scopes"`
	SweepNeverSucceededScopes int `json:"sweep_never_succeeded_scopes"`
	Expected                  int `json:"expected"`
	Observed                  int `json:"observed"`
	Complete                  int `json:"complete"`
	Missing                   int `json:"missing"`
	Stale                     int `json:"stale"`
	Conflicting               int `json:"conflicting"`
	Unavailable               int `json:"unavailable"`
	Unexpected                int `json:"unexpected"`
	NonemptyScopes            int `json:"nonempty_scopes"`
	EmptyScopes               int `json:"empty_scopes"`
	UnknownAgeScopes          int `json:"unknown_age_scopes"`
	UnsuccessfulCollections   int `json:"unsuccessful_collections"`
	EnabledScopes             int `json:"enabled_scopes"`
	DisabledScopes            int `json:"disabled_scopes"`
}

type RuntimeCommandCronClassCounts struct {
	Class            string `json:"class"`
	NonemptyScopes   int    `json:"nonempty_scopes"`
	EmptyScopes      int    `json:"empty_scopes"`
	UnknownAgeScopes int    `json:"unknown_age_scopes"`
}

const RuntimeCommandCronScope = "scheduler_ownership_scopes"

type RuntimeCommandCronSummary struct {
	Scope      string                          `json:"scope"`
	State      string                          `json:"state"`
	Reason     string                          `json:"reason,omitempty"`
	Counts     RuntimeCommandCronCounts        `json:"counts"`
	Classes    []RuntimeCommandCronClassCounts `json:"classes"`
	Incomplete bool                            `json:"incomplete"`
}

type RuntimeCommandCronHealth struct {
	RuntimeCommandCronSummary
	Items        []RuntimeCommandCronItem `json:"items"`
	OmittedItems int                      `json:"omitted_items"`
}

type RuntimeCommandCronObservation struct {
	RuntimeHandoffSignal
	Values *RuntimeCommandCronObservationValues `json:"values,omitempty"`
}

type RuntimeCommandCronObservationValues struct {
	Enabled bool `json:"enabled"`
}

type RuntimeCommandCronClass struct {
	RuntimeHandoffSignal
	Class  string                         `json:"class"`
	Values *RuntimeCommandCronClassValues `json:"values,omitempty"`
}

type RuntimeCommandCronClassValues struct {
	Schedules                int64      `json:"schedules"`
	UnknownAge               int64      `json:"unknown_age"`
	OldestDueAt              *time.Time `json:"oldest_due_at,omitempty"`
	OldestKnownDueAgeSeconds *float64   `json:"oldest_known_due_age_seconds,omitempty"`
}

type RuntimeCommandCronItem struct {
	Job              string                        `json:"job"`
	Node             string                        `json:"node,omitempty"`
	TargetID         string                        `json:"target_id,omitempty"`
	Unexpected       bool                          `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                    `json:"scrape_observed_at,omitempty"`
	State            string                        `json:"state"`
	Collection       RuntimeHandoffCollection      `json:"collection"`
	Sweep            RuntimeCommandCronSweep       `json:"sweep"`
	Observation      RuntimeCommandCronObservation `json:"observation"`
	Classes          []RuntimeCommandCronClass     `json:"classes"`
}

func runtimeCommandCronClasses() []string {
	return []string{"due", "overlap_due", "replacement_wait", "deployment_missing", "exclusive_due", "disabled", "suspended", "account_held", "not_due", "unknown"}
}

func newRuntimeCommandCronSummary() RuntimeCommandCronSummary {
	h := RuntimeCommandCronSummary{Scope: RuntimeCommandCronScope}
	for _, class := range runtimeCommandCronClasses() {
		h.Classes = append(h.Classes, RuntimeCommandCronClassCounts{Class: class})
	}
	return h
}

func CloneRuntimeCommandCronHealth(in *RuntimeCommandCronHealth) *RuntimeCommandCronHealth {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeCommandCronHealth
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func CloneRuntimeCommandCronSummary(in *RuntimeCommandCronSummary) *RuntimeCommandCronSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Classes = append([]RuntimeCommandCronClassCounts{}, in.Classes...)
	return &out
}

func runtimeCommandCronJobs(role string) []string {
	if role == "single-box-v2" {
		return []string{"schedd"}
	}
	if role == "control-plane-v2" {
		return []string{"schedd", "schedd-compute"}
	}
	return nil
}

func runtimeCommandCronDueClass(name string) bool {
	return name == "due" || name == "overlap_due" || name == "replacement_wait" || name == "deployment_missing" || name == "exclusive_due"
}
func runtimeCommandCronInactiveClass(name string) bool {
	return name == "disabled" || name == "suspended" || name == "account_held" || name == "not_due"
}
func runtimeCommandCronUnknownAgeClass(name string) bool {
	return runtimeCommandCronInactiveClass(name) || name == "unknown"
}

type RuntimeCommandCronSweep struct {
	RuntimeHandoffSignal
	Values *RuntimeCommandCronSweepValues `json:"values,omitempty"`
}
type RuntimeCommandCronSweepValues struct {
	Running       bool       `json:"running"`
	LastStartedAt *time.Time `json:"last_started_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
}
