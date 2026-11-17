package operatorobs

import (
	"encoding/json"
	"time"
)

// All aggregates count observation scopes, never attempts across scheduler replicas.
type RuntimeSnapshotPrimeCounts struct {
	Expected                int `json:"expected"`
	Observed                int `json:"observed"`
	Complete                int `json:"complete"`
	Missing                 int `json:"missing"`
	Stale                   int `json:"stale"`
	Conflicting             int `json:"conflicting"`
	Unavailable             int `json:"unavailable"`
	Unexpected              int `json:"unexpected"`
	NonemptyScopes          int `json:"nonempty_scopes"`
	EmptyScopes             int `json:"empty_scopes"`
	UnknownAgeScopes        int `json:"unknown_age_scopes"`
	UnsuccessfulCollections int `json:"unsuccessful_collections"`
	EnabledScopes           int `json:"enabled_scopes"`
	DisabledScopes          int `json:"disabled_scopes"`
}

type RuntimeSnapshotPrimeClassCounts struct {
	Class            string `json:"class"`
	NonemptyScopes   int    `json:"nonempty_scopes"`
	EmptyScopes      int    `json:"empty_scopes"`
	UnknownAgeScopes int    `json:"unknown_age_scopes"`
}

type RuntimeSnapshotPrimeSummary struct {
	State      string                            `json:"state"`
	Reason     string                            `json:"reason,omitempty"`
	Counts     RuntimeSnapshotPrimeCounts        `json:"counts"`
	Classes    []RuntimeSnapshotPrimeClassCounts `json:"classes"`
	Incomplete bool                              `json:"incomplete"`
}

type RuntimeSnapshotPrimeHealth struct {
	RuntimeSnapshotPrimeSummary
	Items        []RuntimeSnapshotPrimeItem `json:"items"`
	OmittedItems int                        `json:"omitted_items"`
}

type RuntimeSnapshotPrimeObservation struct {
	RuntimeHandoffSignal
	Values *RuntimeSnapshotPrimeObservationValues `json:"values,omitempty"`
}

type RuntimeSnapshotPrimeObservationValues struct {
	Enabled bool `json:"enabled"`
}

type RuntimeSnapshotPrimeClass struct {
	RuntimeHandoffSignal
	Class  string                           `json:"class"`
	Values *RuntimeSnapshotPrimeClassValues `json:"values,omitempty"`
}

type RuntimeSnapshotPrimeClassValues struct {
	Pending               int64      `json:"pending"`
	UnknownAge            int64      `json:"unknown_age"`
	OldestAttemptAt       *time.Time `json:"oldest_attempt_at,omitempty"`
	OldestKnownAgeSeconds *float64   `json:"oldest_known_age_seconds,omitempty"`
}

type RuntimeSnapshotPrimeItem struct {
	Job              string                          `json:"job"`
	Node             string                          `json:"node,omitempty"`
	TargetID         string                          `json:"target_id,omitempty"`
	Unexpected       bool                            `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                      `json:"scrape_observed_at,omitempty"`
	State            string                          `json:"state"`
	Collection       RuntimeHandoffCollection        `json:"collection"`
	Observation      RuntimeSnapshotPrimeObservation `json:"observation"`
	Classes          []RuntimeSnapshotPrimeClass     `json:"classes"`
}

func runtimeSnapshotPrimeClasses() []string {
	return []string{"waiting", "release_blocked", "active", "publication_pending", "snapshot_recorded", "unknown"}
}

func newRuntimeSnapshotPrimeSummary() RuntimeSnapshotPrimeSummary {
	h := RuntimeSnapshotPrimeSummary{}
	for _, class := range runtimeSnapshotPrimeClasses() {
		h.Classes = append(h.Classes, RuntimeSnapshotPrimeClassCounts{Class: class})
	}
	return h
}

func CloneRuntimeSnapshotPrimeHealth(in *RuntimeSnapshotPrimeHealth) *RuntimeSnapshotPrimeHealth {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeSnapshotPrimeHealth
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func CloneRuntimeSnapshotPrimeSummary(in *RuntimeSnapshotPrimeSummary) *RuntimeSnapshotPrimeSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Classes = append([]RuntimeSnapshotPrimeClassCounts{}, in.Classes...)
	return &out
}

func runtimeSnapshotPrimeJobs(role string) []string {
	switch role {
	case "single-box-v2":
		return []string{"schedd"}
	case "control-plane-v2":
		return []string{"schedd", "schedd-compute"}
	default:
		return nil
	}
}
