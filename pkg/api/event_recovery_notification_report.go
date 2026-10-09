package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Recovery notification reporting is metadata-only and never captures events.
type EventRecoveryNotifications struct {
	JobID         string                      `json:"job_id"`
	AppID         string                      `json:"app_id"`
	AppSlug       string                      `json:"app_slug"`
	ObservedAt    time.Time                   `json:"observed_at"`
	ReceiverLimit int                         `json:"receiver_limit"`
	Notifications []EventRecoveryNotification `json:"notifications"`
}
type EventRecoveryNotification struct {
	Kind                   string                              `json:"kind"`
	Event                  string                              `json:"event,omitempty"`
	EventID                string                              `json:"event_id,omitempty"`
	CaptureStatus          string                              `json:"capture_status"`
	CapturedAt             *time.Time                          `json:"captured_at,omitempty"`
	EvidenceSource         string                              `json:"evidence_source"`
	RecipientsKnown        bool                                `json:"recipients_known"`
	SelectedRecipientCount *int64                              `json:"selected_recipient_count,omitempty"`
	CountsComplete         bool                                `json:"counts_complete"`
	AcknowledgementStatus  string                              `json:"acknowledgement_status"`
	PendingCount           int64                               `json:"pending_count"`
	InFlightCount          int64                               `json:"in_flight_count"`
	SucceededCount         int64                               `json:"succeeded_count"`
	FailedCount            int64                               `json:"failed_count"`
	DeadCount              int64                               `json:"dead_count"`
	AwaitingRelayCount     int64                               `json:"awaiting_relay_count"`
	UnknownCount           int64                               `json:"unknown_count"`
	Receivers              []EventRecoveryNotificationReceiver `json:"receivers"`
}
type EventRecoveryNotificationReceiver struct {
	WebhookID         string     `json:"webhook_id"`
	ReceiverAvailable bool       `json:"receiver_available"`
	DeliveryID        string     `json:"delivery_id,omitempty"`
	Status            string     `json:"status"`
	Attempt           int        `json:"attempt"`
	ReplayGeneration  int        `json:"replay_generation"`
	LastResponseCode  int        `json:"last_response_code"`
	NextAttemptAt     *time.Time `json:"next_attempt_at,omitempty"`
	DeliveredAt       *time.Time `json:"delivered_at,omitempty"`
	AttemptsPath      string     `json:"attempts_path,omitempty"`
	RetryPath         string     `json:"retry_path,omitempty"`
}

func (c *Client) GetEventRecoveryNotifications(ctx context.Context, job string) (EventRecoveryNotifications, error) {
	var out EventRecoveryNotifications
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(job)+"/notifications", nil, &out)
	return out, err
}
