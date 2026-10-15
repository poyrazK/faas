package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// Zero retains ordinary HTTP-success acknowledgment for new acceptances.
type EventAcknowledgmentPolicy struct {
	SubscriptionID string `json:"subscription_id"`
	LeaseSeconds   int    `json:"lease_seconds"`
}
type EventAcknowledgmentPolicyRequest struct {
	LeaseSeconds *int `json:"lease_seconds"`
}

// The token is an additional fence, never a substitute for app write authority.
type EventAcknowledgmentRequest struct {
	Token  string          `json:"token"`
	Result json.RawMessage `json:"result,omitempty"`
}
type EventAcknowledgmentStatus struct {
	InvocationID     string     `json:"invocation_id"`
	Attempt          int        `json:"attempt"`
	ReplayGeneration int64      `json:"replay_generation"`
	State            string     `json:"state"`
	ProcessingState  string     `json:"processing_state"`
	LeaseExpiresAt   *time.Time `json:"lease_expires_at,omitempty"`
	DeadlineAt       *time.Time `json:"deadline_at,omitempty"`
}

func acknowledgmentPolicyPath(app, sub string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(sub) + "/acknowledgment-policy"
}
func acknowledgmentPath(app, id string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-deliveries/" + url.PathEscape(id) + "/acknowledgment"
}
func (c *Client) GetEventAcknowledgmentPolicy(ctx context.Context, app, sub string) (EventAcknowledgmentPolicy, error) {
	var out EventAcknowledgmentPolicy
	err := c.do(ctx, http.MethodGet, acknowledgmentPolicyPath(app, sub), nil, &out)
	return out, err
}
func (c *Client) SetEventAcknowledgmentPolicy(ctx context.Context, app, sub string, req EventAcknowledgmentPolicyRequest) (EventAcknowledgmentPolicy, error) {
	var out EventAcknowledgmentPolicy
	err := c.do(ctx, http.MethodPut, acknowledgmentPolicyPath(app, sub), req, &out)
	return out, err
}
func (c *Client) GetEventAcknowledgment(ctx context.Context, app, id string) (EventAcknowledgmentStatus, error) {
	var out EventAcknowledgmentStatus
	err := c.do(ctx, http.MethodGet, acknowledgmentPath(app, id), nil, &out)
	return out, err
}
func (c *Client) RenewEventAcknowledgment(ctx context.Context, app, id string, req EventAcknowledgmentRequest) (EventAcknowledgmentStatus, error) {
	var out EventAcknowledgmentStatus
	err := c.do(ctx, http.MethodPost, acknowledgmentPath(app, id)+"/renew", req, &out)
	return out, err
}
func (c *Client) CompleteEventAcknowledgment(ctx context.Context, app, id string, req EventAcknowledgmentRequest) (EventAcknowledgmentStatus, error) {
	var out EventAcknowledgmentStatus
	err := c.do(ctx, http.MethodPost, acknowledgmentPath(app, id)+"/complete", req, &out)
	return out, err
}
