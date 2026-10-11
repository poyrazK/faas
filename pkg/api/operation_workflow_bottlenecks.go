package api

import "time"

// OperationWorkflowBottlenecks measures observed retained intervals for one workflow instance.
type OperationWorkflowBottlenecks struct {
	EvaluatedAt                   time.Time                               `json:"evaluated_at"`
	HistoryComplete               bool                                    `json:"history_complete"`
	IncompleteReasons             []string                                `json:"incomplete_reasons"`
	ObservedFrom                  *time.Time                              `json:"observed_from,omitempty"`
	ObservedThrough               *time.Time                              `json:"observed_through,omitempty"`
	ReportsInWindow               int                                     `json:"reports_in_window"`
	HistoryTruncated              bool                                    `json:"history_truncated"`
	Ongoing                       bool                                    `json:"ongoing"`
	StateSeconds                  int64                                   `json:"state_seconds"`
	BlockedSeconds                int64                                   `json:"blocked_seconds"`
	VerificationUnknownStartCount int64                                   `json:"verification_unknown_start_count"`
	VerificationWaitSeconds       int64                                   `json:"verification_wait_seconds"`
	States                        []OperationWorkflowStateDuration        `json:"states"`
	Blockers                      []OperationWorkflowBlockerDuration      `json:"blockers"`
	VerificationOwners            []OperationWorkflowVerificationDuration `json:"verification_owners"`
	StatesTruncated               bool                                    `json:"states_truncated"`
	BlockersTruncated             bool                                    `json:"blockers_truncated"`
	VerificationOwnersTruncated   bool                                    `json:"verification_owners_truncated"`
}

type OperationWorkflowStateDuration struct {
	ContractVersion  int    `json:"contract_version"`
	State            string `json:"state"`
	ObservedSeconds  int64  `json:"observed_seconds"`
	ObservationCount int64  `json:"observation_count"`
	Ongoing          bool   `json:"ongoing"`
}

type OperationWorkflowBlockerDuration struct {
	ContractVersion  int    `json:"contract_version"`
	Operation        string `json:"operation"`
	Code             string `json:"code"`
	Owner            string `json:"owner"`
	ObservedSeconds  int64  `json:"observed_seconds"`
	ObservationCount int64  `json:"observation_count"`
	Ongoing          bool   `json:"ongoing"`
}

type OperationWorkflowVerificationDuration struct {
	Owner             string `json:"owner"`
	ObservedSeconds   int64  `json:"observed_seconds"`
	ResolutionCount   int64  `json:"resolution_count"`
	PendingCount      int64  `json:"pending_count"`
	UnknownStartCount int64  `json:"unknown_start_count"`
}
