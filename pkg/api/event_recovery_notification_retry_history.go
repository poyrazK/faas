package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type EventRecoveryNotificationRetryHistory struct {
	JobID      string                                          `json:"job_id"`
	AppID      string                                          `json:"app_id"`
	ObservedAt time.Time                                       `json:"observed_at"`
	Decisions  []EventRecoveryNotificationRetryDecisionSummary `json:"decisions"`
}
type EventRecoveryNotificationRetryDecisionSummary struct {
	RequestID    string    `json:"request_id"`
	DecidedAt    time.Time `json:"decided_at"`
	TargetCount  int       `json:"target_count"`
	QueuedCount  int       `json:"queued_count"`
	SkippedCount int       `json:"skipped_count"`
}
type EventRecoveryNotificationRetryDecision struct {
	Target                  EventRecoveryNotificationRetryTarget `json:"target"`
	State                   string                               `json:"state"`
	Reason                  string                               `json:"reason,omitempty"`
	ReplayGeneration        *int                                 `json:"replay_generation,omitempty"`
	CurrentDeliveryStatus   string                               `json:"current_delivery_status"`
	CurrentReplayGeneration *int                                 `json:"current_replay_generation,omitempty"`
}
type EventRecoveryNotificationRetryDecisionDetail struct {
	JobID                   string                                   `json:"job_id"`
	AppID                   string                                   `json:"app_id"`
	RequestID               string                                   `json:"request_id"`
	DecidedAt               time.Time                                `json:"decided_at"`
	CurrentStatusObservedAt time.Time                                `json:"current_status_observed_at"`
	Decisions               []EventRecoveryNotificationRetryDecision `json:"decisions"`
}

func (c *Client) ListEventRecoveryNotificationRetryHistory(ctx context.Context, id string) (EventRecoveryNotificationRetryHistory, error) {
	var out EventRecoveryNotificationRetryHistory
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(id)+"/notification-retry-decisions", nil, &out)
	return out, err
}
func (c *Client) GetEventRecoveryNotificationRetryDecision(ctx context.Context, id, requestID string) (EventRecoveryNotificationRetryDecisionDetail, error) {
	var out EventRecoveryNotificationRetryDecisionDetail
	path := "/v1/event-recoveries/" + url.PathEscape(id) + "/notification-retry-decisions/" + url.PathEscape(requestID)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
