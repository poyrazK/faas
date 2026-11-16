package api

import "time"

// ObsNodeProbeNotifications is a complete, bounded lookup for one selected
// node, independent of the fleet preview and of registry existence.
type ObsNodeProbeNotifications struct {
	GeneratedAt         time.Time                     `json:"generated_at"`
	Scope               string                        `json:"scope"`
	Node                string                        `json:"node"`
	State               string                        `json:"state"`
	Reason              string                        `json:"reason,omitempty"`
	MaxAgeSeconds       int                           `json:"max_age_seconds"`
	Inventory           ObsServingProbeInventory      `json:"inventory"`
	Expected            *bool                         `json:"expected"`
	MaxResultAgeSeconds *float64                      `json:"max_result_age_seconds"`
	Channels            []ObsServingProbeNotification `json:"channels"`
}

// ObsServingProbeNotificationHealth describes completed transport evidence,
// independently of serving health. Counts cover all expected node/channels.
type ObsServingProbeNotificationHealth struct {
	State            string                             `json:"state"`
	Reason           string                             `json:"reason,omitempty"`
	ExpectedChannels *int                               `json:"expected_channels"`
	Counts           *ObsServingProbeNotificationCounts `json:"counts"`
	Limit            int                                `json:"limit"`
	Omitted          int                                `json:"omitted"`
	Truncated        bool                               `json:"truncated"`
	Channels         []ObsServingProbeNotification      `json:"channels"`
}

type ObsServingProbeNotificationCounts struct {
	Disabled                int `json:"disabled"`
	Unknown                 int `json:"unknown"`
	LastSubmissionSucceeded int `json:"last_submission_succeeded"`
	LastSubmissionFailed    int `json:"last_submission_failed"`
}

type ObsServingProbeNotification struct {
	Node           string     `json:"node"`
	Channel        string     `json:"channel"`
	State          string     `json:"state"`
	Reason         string     `json:"reason,omitempty"`
	EvidenceReason string     `json:"evidence_reason,omitempty"`
	Enabled        *bool      `json:"enabled"`
	EvaluatedAt    *time.Time `json:"evaluated_at"`
	LastRunAt      *time.Time `json:"last_run_at"`
	LastAttemptAt  *time.Time `json:"last_attempt_at"`
	LastSuccessAt  *time.Time `json:"last_success_at"`
	FailureCode    string     `json:"failure_code,omitempty"`
}
