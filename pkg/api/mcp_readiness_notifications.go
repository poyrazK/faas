package api

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Recovery receipts are durable decisions, not a promise of webhook acceptance.
type MCPReadinessNotificationAction struct {
	Destination     string `json:"destination,omitempty"`
	RequestID       string `json:"request_id"`
	EventID         string `json:"event_id"`
	Action          string `json:"action"`
	ExpectedVersion *int64 `json:"expected_version"`
	Reason          string `json:"reason"`
}
type MCPReadinessNotificationReceipt struct {
	Destination     string    `json:"destination,omitempty"`
	RequestID       string    `json:"request_id"`
	EventID         string    `json:"event_id"`
	Action          string    `json:"action"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedBy       string    `json:"created_by"`
	DeliveryVersion int64     `json:"delivery_version"`
}
type MCPReadinessNotificationQueue struct {
	Destinations     []MCPReadinessDestinationQueue `json:"destinations,omitempty"`
	RuleID           string                         `json:"rule_id"`
	Name             string                         `json:"name"`
	ReadinessScope   string                         `json:"mcp_readiness_scope,omitempty"`
	Enabled          bool                           `json:"enabled"`
	OutstandingCount int64                          `json:"outstanding_count"`
	BlockedCount     int64                          `json:"blocked_count"`
	OldestAgeSeconds float64                        `json:"oldest_age_seconds"`
	State            string                         `json:"state"`
	FailureKind      string                         `json:"failure_kind,omitempty"`
	CanRetry         bool                           `json:"can_retry"`
	CanAbandon       bool                           `json:"can_abandon"`
	Head             *MCPReadinessIncidentEvent     `json:"head,omitempty"`
}

// Destination delivery progress never includes lease tokens, addresses or credentials.
type MCPReadinessDestinationDelivery struct {
	Provider       string     `json:"provider,omitempty"`
	ProviderStatus string     `json:"provider_status,omitempty"`
	Destination    string     `json:"destination"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	LastStatusCode int        `json:"last_status_code,omitempty"`
	LastDeliveryID string     `json:"last_delivery_id,omitempty"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	LeaseUntil     *time.Time `json:"lease_until,omitempty"`
}
type MCPReadinessDestinationQueue struct {
	Provider         string                     `json:"provider,omitempty"`
	Destination      string                     `json:"destination"`
	OutstandingCount int64                      `json:"outstanding_count"`
	BlockedCount     int64                      `json:"blocked_count"`
	State            string                     `json:"state"`
	FailureKind      string                     `json:"failure_kind,omitempty"`
	CanRetry         bool                       `json:"can_retry"`
	CanAbandon       bool                       `json:"can_abandon"`
	Head             *MCPReadinessIncidentEvent `json:"head,omitempty"`
}
type MCPReadinessNotificationQueues struct {
	ObservedAt time.Time                       `json:"observed_at"`
	Queues     []MCPReadinessNotificationQueue `json:"queues"`
	NextAfter  string                          `json:"next_after,omitempty"`
}

func ValidateMCPReadinessNotificationAction(r MCPReadinessNotificationAction) error {
	if r.Destination != "" && r.Destination != "primary" && r.Destination != "fallback" || uuid.Validate(r.RequestID) != nil || uuid.Validate(r.EventID) != nil || r.ExpectedVersion == nil || *r.ExpectedVersion < 0 || (r.Action != "retry" && r.Action != "abandon") || !ValidMCPMaintenanceReason(r.Reason) {
		return errors.New("supply request and event UUIDs, a nonnegative expected_version, retry or abandon, an optional primary or fallback destination, and a reason of 1–500 bytes")
	}
	return nil
}
func (c *Client) ListMCPReadinessNotificationQueues(ctx context.Context, app, scope, after string, limit int) (MCPReadinessNotificationQueues, error) {
	q := url.Values{}
	if scope != "" {
		q.Set("scope", scope)
	}
	if after != "" {
		q.Set("after", after)
	}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out MCPReadinessNotificationQueues
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-notifications?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) ActMCPReadinessNotification(ctx context.Context, app string, r MCPReadinessNotificationAction) (MCPReadinessNotificationReceipt, error) {
	var out MCPReadinessNotificationReceipt
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-notifications/actions", r, &out)
	return out, err
}
