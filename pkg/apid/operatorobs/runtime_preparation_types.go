package operatorobs

import (
	"encoding/json"
	"time"
)

// All aggregates count observation scopes, never checkpoints across replicas.
type RuntimePreparationCounts struct {
	Expected                int `json:"expected"`
	Observed                int `json:"observed"`
	Complete                int `json:"complete"`
	Missing                 int `json:"missing"`
	Stale                   int `json:"stale"`
	Conflicting             int `json:"conflicting"`
	Unavailable             int `json:"unavailable"`
	Unexpected              int `json:"unexpected"`
	WaitingScopes           int `json:"waiting_scopes"`
	EmptyScopes             int `json:"empty_scopes"`
	UnknownAgeScopes        int `json:"unknown_age_scopes"`
	UnsuccessfulCollections int `json:"unsuccessful_collections"`
	EnabledScopes           int `json:"enabled_scopes"`
	DisabledScopes          int `json:"disabled_scopes"`
}

type RuntimePreparationPhaseCounts struct {
	Phase            string `json:"phase"`
	WaitingScopes    int    `json:"waiting_scopes"`
	EmptyScopes      int    `json:"empty_scopes"`
	UnknownAgeScopes int    `json:"unknown_age_scopes"`
}

type RuntimePreparationSummary struct {
	State      string                          `json:"state"`
	Reason     string                          `json:"reason,omitempty"`
	Counts     RuntimePreparationCounts        `json:"counts"`
	Phases     []RuntimePreparationPhaseCounts `json:"phases"`
	Incomplete bool                            `json:"incomplete"`
}

type RuntimePreparationHealth struct {
	RuntimePreparationSummary
	Items        []RuntimePreparationItem `json:"items"`
	OmittedItems int                      `json:"omitted_items"`
}

type RuntimePreparationObservation struct {
	RuntimeHandoffSignal
	Values *RuntimePreparationObservationValues `json:"values,omitempty"`
}

type RuntimePreparationObservationValues struct {
	Enabled bool `json:"enabled"`
}

type RuntimePreparationPhase struct {
	RuntimeHandoffSignal
	Phase  string                         `json:"phase"`
	Values *RuntimePreparationPhaseValues `json:"values,omitempty"`
}

type RuntimePreparationPhaseValues struct {
	Pending               int64      `json:"pending"`
	UnknownAge            int64      `json:"unknown_age"`
	OldestPhaseStartedAt  *time.Time `json:"oldest_phase_started_at,omitempty"`
	OldestKnownAgeSeconds *float64   `json:"oldest_known_age_seconds,omitempty"`
}

type RuntimePreparationItem struct {
	Node             string                        `json:"node,omitempty"`
	TargetID         string                        `json:"target_id,omitempty"`
	Unexpected       bool                          `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                    `json:"scrape_observed_at,omitempty"`
	State            string                        `json:"state"`
	Collection       RuntimeHandoffCollection      `json:"collection"`
	Observation      RuntimePreparationObservation `json:"observation"`
	Phases           []RuntimePreparationPhase     `json:"phases"`
}

func runtimePreparationPhases() []string {
	return []string{"preparing", "layer_published", "scan_complete"}
}

func newRuntimePreparationSummary() RuntimePreparationSummary {
	h := RuntimePreparationSummary{}
	for _, phase := range runtimePreparationPhases() {
		h.Phases = append(h.Phases, RuntimePreparationPhaseCounts{Phase: phase})
	}
	return h
}

func CloneRuntimePreparationHealth(in *RuntimePreparationHealth) *RuntimePreparationHealth {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimePreparationHealth
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func CloneRuntimePreparationSummary(in *RuntimePreparationSummary) *RuntimePreparationSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Phases = append([]RuntimePreparationPhaseCounts{}, in.Phases...)
	return &out
}
