package api

import "time"

// Status describes monitoring, independently of a workflow's execution outcome.
type WorkflowRecoveryPollingWebhookPayload struct {
	EventID        string     `json:"event_id"`
	AppID          string     `json:"app_id"`
	App            string     `json:"app"`
	Scope          string     `json:"scope"`
	ReviewID       string     `json:"review_id,omitempty"`
	PolicyVersion  int64      `json:"policy_version"`
	Sequence       int64      `json:"sequence"`
	State          string     `json:"state"`
	Code           string     `json:"code"`
	Attempts       int64      `json:"attempts"`
	ObservedAt     time.Time  `json:"observed_at"`
	RetryAt        *time.Time `json:"retry_at,omitempty"`
	InspectionURL  string     `json:"inspection_url"`
	MonitoringOnly bool       `json:"monitoring_only"`
}

type WorkflowRecoveryPollingNotification struct {
	Payload            WorkflowRecoveryPollingWebhookPayload `json:"payload"`
	Status             string                                `json:"status"`
	CapturedAt         time.Time                             `json:"captured_at"`
	ReceiverWebhookIDs []string                              `json:"receiver_webhook_ids"`
}
