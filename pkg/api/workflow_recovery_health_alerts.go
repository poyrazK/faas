package api

import (
	"fmt"
	"time"
)

// WorkflowRecoveryHealthAlertPolicy opts into scheduler-owned incident notifications.
type WorkflowRecoveryHealthAlertPolicy struct {
	Enabled           bool `json:"enabled"`
	StallAfterSeconds int  `json:"stall_after_seconds"`
	AlertAfterSeconds int  `json:"alert_after_seconds"`
}

func (p WorkflowRecoveryHealthAlertPolicy) Validate() error {
	if err := ValidateRecoveryHealthThreshold(p.StallAfterSeconds); err != nil {
		return err
	}
	if p.AlertAfterSeconds < WorkflowRecoveryHealthMinStallSeconds || p.AlertAfterSeconds > WorkflowRecoveryHealthMaxStallSeconds {
		return fmt.Errorf("alert_after_seconds must be between %d and %d", WorkflowRecoveryHealthMinStallSeconds, WorkflowRecoveryHealthMaxStallSeconds)
	}
	return nil
}

// WorkflowRecoveryHealthNotification retains the captured delivery intent.
type WorkflowRecoveryHealthNotification struct {
	EventID            string    `json:"event_id"`
	Status             string    `json:"status"`
	CapturedAt         time.Time `json:"captured_at"`
	ReceiverWebhookIDs []string  `json:"receiver_webhook_ids"`
}

// WorkflowRecoveryHealthIncident is the latest app incident, retained across policy replacements.
type WorkflowRecoveryHealthIncident struct {
	EpisodeID            string                              `json:"episode_id"`
	OpenedAt             time.Time                           `json:"opened_at"`
	CandidateSince       time.Time                           `json:"candidate_since"`
	ObservedAt           time.Time                           `json:"observed_at"`
	Status               string                              `json:"status"`
	Reason               string                              `json:"reason"`
	ClosedAt             *time.Time                          `json:"closed_at,omitempty"`
	AlertNotification    *WorkflowRecoveryHealthNotification `json:"alert_notification,omitempty"`
	RecoveryNotification *WorkflowRecoveryHealthNotification `json:"recovery_notification,omitempty"`
}

// WorkflowRecoveryHealthWebhookPayload is a historical incident transition without workflow data.
type WorkflowRecoveryHealthWebhookPayload struct {
	EventID        string    `json:"event_id"`
	EpisodeID      string    `json:"episode_id"`
	AppID          string    `json:"app_id"`
	App            string    `json:"app"`
	PolicyVersion  int64     `json:"policy_version"`
	Status         string    `json:"status"`
	Reason         string    `json:"reason"`
	OpenedAt       time.Time `json:"opened_at"`
	ObservedAt     time.Time `json:"observed_at"`
	Pending        int       `json:"pending"`
	Outstanding    int       `json:"outstanding"`
	QueueTruncated bool      `json:"queue_truncated"`
	HealthURL      string    `json:"health_url"`
}
