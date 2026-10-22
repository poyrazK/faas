package api

import "time"

// WorkflowRecoveryOverview describes retained recovery observations, not liveness or workflow success.
type WorkflowRecoveryOverview struct {
	ManualRetryAwaitingReview int                             `json:"manual_retry_awaiting_review"`
	ManualRetryUnresolved     int                             `json:"manual_retry_unresolved"`
	Status                    string                          `json:"status"`
	MonitoringStatus          string                          `json:"monitoring_status"`
	ObservedAt                time.Time                       `json:"observed_at"`
	MonitoringOnly            bool                            `json:"monitoring_only"`
	ManualAwaitingApproval    int                             `json:"manual_awaiting_approval"`
	ManualUnresolved          int                             `json:"manual_unresolved"`
	ManualPollingFailures     int                             `json:"manual_polling_failures"`
	ManualAttention           int                             `json:"manual_attention"`
	Truncated                 bool                            `json:"truncated"`
	Issues                    []WorkflowRecoveryOverviewIssue `json:"issues"`
	LastAlert                 *WorkflowRecoveryOverviewAlert  `json:"last_alert,omitempty"`
}

type WorkflowRecoveryOverviewIssue struct {
	Scope         string     `json:"scope"`
	Reason        string     `json:"reason"`
	PolicyVersion int64      `json:"policy_version"`
	ReviewID      string     `json:"review_id,omitempty"`
	JobID         string     `json:"job_id,omitempty"`
	RetryAt       *time.Time `json:"retry_at,omitempty"`
	InspectionURL string     `json:"inspection_url"`
}

// Counts cover only receivers frozen in the captured receipt, excluding later subscribers.
type WorkflowRecoveryOverviewAlert struct {
	Notification   WorkflowRecoveryPollingNotification `json:"notification"`
	DeliveryStatus string                              `json:"delivery_status"`
	Expected       int                                 `json:"expected"`
	Pending        int                                 `json:"pending"`
	InFlight       int                                 `json:"in_flight"`
	Succeeded      int                                 `json:"succeeded"`
	Failed         int                                 `json:"failed"`
	Dead           int                                 `json:"dead"`
	Missing        int                                 `json:"missing"`
	RelayPending   bool                                `json:"relay_pending"`
}
