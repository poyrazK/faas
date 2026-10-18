package api

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strconv"
	"time"
)

const MCPReadinessPagerDutyWebhookSecretNamespace = "mcp_readiness_pagerduty_webhook_v1"

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

func ValidMCPReadinessPagerDutyProviderID(value string) bool {
	if len(value) == 0 || len(value) > MCPReadinessPagerDutyWebhookIDMaxBytes {
		return false
	}
	for _, c := range value {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !valid {
			return false
		}
	}
	return true
}
func ValidMCPReadinessPagerDutySigningSecret(value string) bool {
	if len(value) < MCPReadinessPagerDutyWebhookSecretMinBytes || len(value) > MCPReadinessPagerDutyWebhookSecretMaxBytes {
		return false
	}
	for _, c := range value {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func ValidateMCPReadinessPagerDutyAcknowledgementsRequest(r SetMCPReadinessPagerDutyAcknowledgementsRequest) error {
	if r.ExpectedVersion == nil || *r.ExpectedVersion < 0 || *r.ExpectedVersion == math.MaxInt64 || r.Enabled == nil {
		return errors.New("supply expected_version and enabled")
	}
	if *r.Enabled {
		if !ValidMCPReadinessPagerDutyProviderID(r.ServiceID) || !ValidMCPReadinessPagerDutySigningSecret(r.SigningSecret) {
			return errors.New("enable with service ID and webhook signing secret")
		}
		if r.Destination == "fallback" {
			if r.ExpectedFallbackVersion == nil || *r.ExpectedFallbackVersion < 1 || r.ExpectedPagerDutyVersion != nil {
				return errors.New("fallback acknowledgement sync requires expected_fallback_version; omit expected_pagerduty_version")
			}
		} else if r.Destination != "" && r.Destination != "primary" || r.ExpectedFallbackVersion != nil || r.ExpectedPagerDutyVersion == nil || *r.ExpectedPagerDutyVersion < 1 {
			return errors.New("primary acknowledgement sync requires expected_pagerduty_version; omit expected_fallback_version")
		}
	} else if r.SyncUnacknowledgements || (r.Destination != "" && r.Destination != "primary" && r.Destination != "fallback") || r.ExpectedFallbackVersion != nil || r.ExpectedPagerDutyVersion != nil || r.ServiceID != "" || r.SigningSecret != "" {
		return errors.New("omit binding and signing secret when disabling")
	}
	return nil
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
