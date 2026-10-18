package api

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
)

// SetMCPReadinessFallbackRequest replaces the policy for incidents whose first
// delivery has not been claimed. Secrets are write-only and required on enable.
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

// MCPReadinessDeliveryRouting contains no receiver credentials or addresses.
// Counts describe dispatch claims; failed claims count only after fenced completion.
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

func ValidateMCPReadinessFallbackRequest(r SetMCPReadinessFallbackRequest) error {
	if r.ExpectedVersion == nil || *r.ExpectedVersion < 0 || *r.ExpectedVersion == math.MaxInt64 {
		return errors.New("expected_version is required")
	}
	if r.Enabled == nil {
		return errors.New("enabled is required")
	}
	if !*r.Enabled {
		if r.Provider != "" || r.IntegrationKey != "" || r.Region != "" || r.WebhookURL != "" || r.WebhookSecret != "" || r.AfterFailures != 0 {
			return errors.New("disabled policy must omit destination fields")
		}
		return nil
	}
	if r.AfterFailures < 1 || r.AfterFailures > MCPReadinessFallbackMaxFailures {
		return errors.New("supply a failure threshold of 1–10")
	}
	if r.Provider == "pagerduty" {
		_, regionOK := MCPReadinessPagerDutyEndpointForRegion(r.Region)
		if !regionOK || !ValidMCPReadinessPagerDutyKey(r.IntegrationKey) || r.WebhookURL != "" || r.WebhookSecret != "" {
			return errors.New("PagerDuty fallback requires an integration key and us/eu region; omit webhook fields")
		}
		return nil
	}
	if r.Provider != "" && r.Provider != "webhook" || r.IntegrationKey != "" || r.Region != "" {
		return errors.New("select webhook or pagerduty and supply only that provider's fields")
	}
	u, err := url.Parse(r.WebhookURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || strings.TrimSpace(r.WebhookURL) != r.WebhookURL || len(r.WebhookURL) > MCPReadinessFallbackURLMaxBytes {
		return errors.New("invalid fallback URL")
	}
	if strings.TrimSpace(r.WebhookSecret) == "" || len(r.WebhookSecret) > AlertRuleWebhookSecretMaxBytes || r.AfterFailures < 1 || r.AfterFailures > MCPReadinessFallbackMaxFailures {
		return errors.New("supply a signing secret and a failure threshold of 1–10")
	}
	return nil
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
