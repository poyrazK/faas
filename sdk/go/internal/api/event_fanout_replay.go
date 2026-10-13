package api

import (
	"context"
	"net/url"
	"time"
)

type EventRoutingRecoveryExpectation struct {
	WorkflowName     string `json:"workflow_name"`
	PublishedVersion int64  `json:"published_version"`
	ReplayCount      int64  `json:"replay_count"`
	Generation       *int64 `json:"generation,omitempty"`
}

type ReplayEventFanoutFailureRequest struct {
	RecoveryExpectation *EventRoutingRecoveryExpectation `json:"recovery_expectation,omitempty"`
	ExpectedGeneration  *int64                           `json:"expected_generation,omitempty"`
	ExpectedAcceptedAt  *time.Time                       `json:"expected_accepted_at,omitempty"`
	AllowExpired        bool                             `json:"allow_expired,omitempty"`
	EventID             string                           `json:"event_id"`
	EventSource         string                           `json:"event_source"`
	SubscriptionID      string                           `json:"subscription_id"`
}
type ReplayEventFanoutFailureResponse struct {
	EventID        string `json:"event_id"`
	EventSource    string `json:"event_source"`
	SubscriptionID string `json:"subscription_id"`
	State          string `json:"state"`
}

func (c *Client) ReplayEventFanoutFailure(ctx context.Context, slug string, req ReplayEventFanoutFailureRequest) (ReplayEventFanoutFailureResponse, error) {
	var out ReplayEventFanoutFailureResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/event-deliveries:replay-fanout-failure", req, &out)
	return out, err
}
