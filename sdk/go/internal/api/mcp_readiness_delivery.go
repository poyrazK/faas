package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// MCP readiness notification wire contracts mirror pkg/api and api/openapi.yaml.

type SetMCPReadinessFallbackRequest struct {
	Provider        string `json:"provider,omitempty"`
	IntegrationKey  string `json:"integration_key,omitempty"`
	Region          string `json:"region,omitempty"`
	ExpectedVersion *int64 `json:"expected_version"`
	Enabled         *bool  `json:"enabled"`
	WebhookURL      string `json:"webhook_url,omitempty"`
	WebhookSecret   string `json:"webhook_secret,omitempty"`
	AfterFailures   int    `json:"after_failures,omitempty"`
}

type MCPReadinessFallbackPolicy struct {
	Provider                  string `json:"provider,omitempty"`
	Region                    string `json:"region,omitempty"`
	IntegrationKeyMasked      string `json:"integration_key_masked,omitempty"`
	Version                   int64  `json:"version"`
	Enabled                   bool   `json:"enabled"`
	WebhookURL                string `json:"webhook_url,omitempty"`
	WebhookSecretSealedMasked string `json:"webhook_secret_sealed_masked,omitempty"`
	AfterFailures             int    `json:"after_failures,omitempty"`
}

type MCPReadinessDeliveryRouting struct {
	PagerDutyVersion  int64  `json:"pagerduty_version,omitempty"`
	PolicyVersion     int64  `json:"policy_version"`
	LastDestination   string `json:"last_destination,omitempty"`
	FailoverReason    string `json:"failover_reason,omitempty"`
	PrimaryAttempts   int    `json:"primary_attempts"`
	FallbackAttempts  int    `json:"fallback_attempts"`
	PrimaryFailures   int    `json:"primary_failures"`
	PrimaryDelivered  bool   `json:"primary_delivered"`
	FallbackDelivered bool   `json:"fallback_delivered"`
}

func (c *Client) GetMCPReadinessFallback(ctx context.Context, app, rule string) (MCPReadinessFallbackPolicy, error) {
	var out MCPReadinessFallbackPolicy
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-fallback", nil, &out)
	return out, err
}
func (c *Client) SetMCPReadinessFallback(ctx context.Context, app, rule string, request SetMCPReadinessFallbackRequest) (MCPReadinessFallbackPolicy, error) {
	var out MCPReadinessFallbackPolicy
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-fallback", request, &out)
	return out, err
}

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

type MCPReadinessIncidentEvent struct {
	DestinationDeliveries []MCPReadinessDestinationDelivery `json:"destination_deliveries,omitempty"`
	DeliveryRouting       *MCPReadinessDeliveryRouting      `json:"delivery_routing,omitempty"`
	ReminderNumber        int                               `json:"reminder_number,omitempty"`
	DeliveryVersion       int64                             `json:"delivery_version"`
	LastOperatorAction    *MCPReadinessNotificationReceipt  `json:"last_operator_action,omitempty"`
	ID                    string                            `json:"id"`
	IncidentID            string                            `json:"incident_id"`
	EventID               string                            `json:"event_id"`
	Event                 string                            `json:"event"`
	RuleID                string                            `json:"rule_id"`
	ReadinessScope        string                            `json:"mcp_readiness_scope,omitempty"`
	Scopes                []string                          `json:"scopes"`
	OccurredAt            time.Time                         `json:"occurred_at"`
	AttemptedAt           *time.Time                        `json:"attempted_at,omitempty"`
	IncidentStatus        string                            `json:"incident_status"`
	AttemptCount          int                               `json:"attempt_count"`
	NextAttemptAt         *time.Time                        `json:"next_attempt_at,omitempty"`
	LastDeliveryID        string                            `json:"last_delivery_id,omitempty"`
	LastStatusCode        int                               `json:"last_status_code,omitempty"`
	DeliveryStatus        string                            `json:"delivery_status"`
	MaintenanceWindowIDs  []string                          `json:"maintenance_window_ids,omitempty"`
	SuppressionReason     string                            `json:"suppression_reason,omitempty"`
	NotificationReason    string                            `json:"notification_reason,omitempty"`
	Subjects              []MCPReadinessMonitorStatus       `json:"subjects"`
}

type MCPReadinessIncidentTimeline struct {
	Events     []MCPReadinessIncidentEvent `json:"events"`
	NextBefore string                      `json:"next_before,omitempty"`
}

func (c *Client) ListMCPReadinessIncidents(ctx context.Context, app, scope, before string, limit int) (MCPReadinessIncidentTimeline, error) {
	q := url.Values{}
	if scope != "" {
		q.Set("scope", scope)
	}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out MCPReadinessIncidentTimeline
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-incidents?"+q.Encode(), nil, &out)
	return out, err
}

type MCPReadinessOAuthConnection struct {
	Scope    string `json:"scope,omitempty"`
	Role     string `json:"role"`
	Revision string `json:"revision"`
}

type MCPReadinessMonitorObservation struct {
	ObservedAt time.Time `json:"observed_at"`
	Passed     bool      `json:"passed"`
	Reason     string    `json:"reason,omitempty"`
}

type MCPReadinessMonitorStatus struct {
	Scope               string                           `json:"scope,omitempty"`
	OAuthConnection     *MCPReadinessOAuthConnection     `json:"oauth_connection,omitempty"`
	RecoveryAction      string                           `json:"recovery_action,omitempty"`
	DeploymentID        string                           `json:"deployment_id"`
	Role                string                           `json:"role"`
	PolicyRevision      string                           `json:"policy_revision"`
	ArtifactSHA256      string                           `json:"artifact_sha256,omitempty"`
	NextProbeAt         time.Time                        `json:"next_probe_at"`
	LastSuccessAt       *time.Time                       `json:"last_success_at,omitempty"`
	ConsecutiveFailures int                              `json:"consecutive_failures"`
	FailureThreshold    int                              `json:"failure_threshold"`
	State               string                           `json:"state"`
	Reason              string                           `json:"reason,omitempty"`
	History             []MCPReadinessMonitorObservation `json:"history"`
}
