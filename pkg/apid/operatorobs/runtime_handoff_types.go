package operatorobs

import (
	"encoding/json"
	"time"
)

// Counts describe imaged observation scopes, which may overlap between replicas.
type RuntimeHandoffCounts struct {
	Expected                 int `json:"expected"`
	Observed                 int `json:"observed"`
	Complete                 int `json:"complete"`
	Missing                  int `json:"missing"`
	Stale                    int `json:"stale"`
	Conflicting              int `json:"conflicting"`
	Unavailable              int `json:"unavailable"`
	Unexpected               int `json:"unexpected"`
	WaitingScopes            int `json:"waiting_scopes"`
	EmptyScopes              int `json:"empty_scopes"`
	UnsuccessfulCollections  int `json:"unsuccessful_collections"`
	WorkingRecoveryScopes    int `json:"working_recovery_scopes"`
	WaitingRecoveryScopes    int `json:"waiting_recovery_scopes"`
	StoppingRecoveryScopes   int `json:"stopping_recovery_scopes"`
	NotStartedRecoveryScopes int `json:"not_started_recovery_scopes"`
}

type RuntimeHandoffSummary struct {
	State      string               `json:"state"`
	Reason     string               `json:"reason,omitempty"`
	Counts     RuntimeHandoffCounts `json:"counts"`
	Incomplete bool                 `json:"incomplete"`
}

type RuntimeHandoffHealth struct {
	RuntimeHandoffSummary
	Items        []RuntimeHandoffItem `json:"items"`
	OmittedItems int                  `json:"omitted_items"`
}

type RuntimeHandoffSignal struct {
	State      string     `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type RuntimeHandoffCollection struct {
	RuntimeHandoffSignal
	Values *RuntimeHandoffCollectionValues `json:"values,omitempty"`
}

type RuntimeHandoffCollectionValues struct {
	Success       bool       `json:"success"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
}

type RuntimeHandoffBacklog struct {
	RuntimeHandoffSignal
	Values *RuntimeHandoffBacklogValues `json:"values,omitempty"`
}

type RuntimeHandoffBacklogValues struct {
	Pending           int64      `json:"pending"`
	OldestFinishedAt  *time.Time `json:"oldest_finished_at,omitempty"`
	OldestWaitSeconds float64    `json:"oldest_wait_seconds"`
}

type RuntimeHandoffRecovery struct {
	RuntimeHandoffSignal
	Values *RuntimeHandoffRecoveryValues `json:"values,omitempty"`
}

type RuntimeHandoffRecoveryValues struct {
	Enabled         bool       `json:"enabled"`
	Inflight        bool       `json:"inflight"`
	LastAttemptAt   *time.Time `json:"last_attempt_at,omitempty"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
}

type RuntimeHandoffItem struct {
	Node             string                   `json:"node,omitempty"`
	TargetID         string                   `json:"target_id,omitempty"`
	Unexpected       bool                     `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time               `json:"scrape_observed_at,omitempty"`
	State            string                   `json:"state"`
	Collection       RuntimeHandoffCollection `json:"collection"`
	Backlog          RuntimeHandoffBacklog    `json:"backlog"`
	Recovery         RuntimeHandoffRecovery   `json:"recovery"`
}

func CloneRuntimeHandoffHealth(in *RuntimeHandoffHealth) *RuntimeHandoffHealth {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeHandoffHealth
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}
