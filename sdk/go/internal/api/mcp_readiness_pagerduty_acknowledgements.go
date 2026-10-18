package api

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"
)

// Webhook credentials are write-only. Disabling revokes them immediately.
type SetMCPReadinessPagerDutyAcknowledgementsRequest struct {
	SyncUnacknowledgements   bool   `json:"sync_unacknowledgements,omitempty"`
	Destination              string `json:"destination,omitempty"`
	ExpectedFallbackVersion  *int64 `json:"expected_fallback_version,omitempty"`
	ExpectedVersion          *int64 `json:"expected_version"`
	Enabled                  *bool  `json:"enabled"`
	ExpectedPagerDutyVersion *int64 `json:"expected_pagerduty_version,omitempty"`
	ServiceID                string `json:"service_id,omitempty"`
	SigningSecret            string `json:"signing_secret,omitempty"`
}
type MCPReadinessPagerDutyAcknowledgementPolicy struct {
	SyncUnacknowledgements bool   `json:"sync_unacknowledgements,omitempty"`
	Destination            string `json:"destination,omitempty"`
	FallbackVersion        int64  `json:"fallback_version,omitempty"`
	Version                int64  `json:"version"`
	Enabled                bool   `json:"enabled"`
	PagerDutyVersion       int64  `json:"pagerduty_version,omitempty"`
	Region                 string `json:"region,omitempty"`
	ServiceID              string `json:"service_id,omitempty"`
	SigningSecretMasked    string `json:"signing_secret_masked,omitempty"`
	WebhookPath            string `json:"webhook_path"`
}
type MCPReadinessPagerDutyAcknowledgementEvent struct {
	Action             string    `json:"action,omitempty"`
	Destination        string    `json:"destination,omitempty"`
	Region             string    `json:"region"`
	ServiceID          string    `json:"service_id"`
	ID                 string    `json:"id"`
	EventID            string    `json:"event_id"`
	IncidentID         string    `json:"incident_id"`
	ProviderIncidentID string    `json:"provider_incident_id"`
	OccurredAt         time.Time `json:"occurred_at"`
	ReceivedAt         time.Time `json:"received_at"`
	PolicyVersion      int64     `json:"policy_version"`
	Outcome            string    `json:"outcome"`
	AttentionVersion   int64     `json:"attention_version"`
}
type MCPReadinessPagerDutyAcknowledgementEvents struct {
	Events     []MCPReadinessPagerDutyAcknowledgementEvent `json:"events"`
	NextBefore string                                      `json:"next_before,omitempty"`
}

func (c *Client) GetMCPReadinessPagerDutyAcknowledgements(ctx context.Context, app, rule string, destination ...string) (MCPReadinessPagerDutyAcknowledgementPolicy, error) {
	var out MCPReadinessPagerDutyAcknowledgementPolicy
	query := ""
	if len(destination) > 1 || len(destination) == 1 && destination[0] != "" && destination[0] != "primary" && destination[0] != "fallback" {
		return out, errors.New("invalid acknowledgement destination")
	}
	if len(destination) == 1 && destination[0] != "" {
		query = "?destination=" + url.QueryEscape(destination[0])
	}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-pagerduty-acknowledgements"+query, nil, &out)
	return out, err
}
func (c *Client) SetMCPReadinessPagerDutyAcknowledgements(ctx context.Context, app, rule string, r SetMCPReadinessPagerDutyAcknowledgementsRequest) (MCPReadinessPagerDutyAcknowledgementPolicy, error) {
	var out MCPReadinessPagerDutyAcknowledgementPolicy
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-pagerduty-acknowledgements", r, &out)
	return out, err
}
func (c *Client) ListMCPReadinessPagerDutyAcknowledgementEvents(ctx context.Context, app, rule, before string, limit int) (MCPReadinessPagerDutyAcknowledgementEvents, error) {
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out MCPReadinessPagerDutyAcknowledgementEvents
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-pagerduty-acknowledgements/events?"+q.Encode(), nil, &out)
	return out, err
}

// An active provider acknowledgement. Integration fingerprints remain private.
type MCPReadinessPagerDutyAcknowledgementHold struct {
	Destination        string    `json:"destination"`
	ProviderIncidentID string    `json:"provider_incident_id"`
	ProviderEventID    string    `json:"provider_event_id"`
	ServiceID          string    `json:"service_id"`
	Region             string    `json:"region"`
	OccurredAt         time.Time `json:"occurred_at"`
	ReceivedAt         time.Time `json:"received_at"`
	PolicyVersion      int64     `json:"policy_version"`
	ReleaseSyncEnabled bool      `json:"release_sync_enabled"`
}
