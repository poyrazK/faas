package api

import (
	"context"
	"errors"
	"math"
	"net/url"
)

const MCPReadinessPagerDutyEndpoint = "https://events.pagerduty.com/v2/enqueue"
const MCPReadinessPagerDutyEUEndpoint = "https://events.eu.pagerduty.com/v2/enqueue"
const MCPReadinessPagerDutySecretNamespace = "mcp_readiness_pagerduty_v1"

// PagerDuty keys are write-only. Settings affect incidents not yet attempted.
type SetMCPReadinessPagerDutyRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Enabled         *bool  `json:"enabled"`
	IntegrationKey  string `json:"integration_key,omitempty"`
	Region          string `json:"region,omitempty"`
}
type MCPReadinessPagerDutyPolicy struct {
	Version              int64  `json:"version"`
	Enabled              bool   `json:"enabled"`
	Region               string `json:"region"`
	IntegrationKeyMasked string `json:"integration_key_masked,omitempty"`
}

// An omitted region preserves the original US default. Endpoints are never user supplied.
func MCPReadinessPagerDutyEndpointForRegion(region string) (string, bool) {
	switch region {
	case "", "us":
		return MCPReadinessPagerDutyEndpoint, true
	case "eu":
		return MCPReadinessPagerDutyEUEndpoint, true
	default:
		return "", false
	}
}

func ValidMCPReadinessPagerDutyKey(key string) bool {
	if len(key) != MCPReadinessPagerDutyKeyBytes {
		return false
	}
	for _, c := range key {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !valid {
			return false
		}
	}
	return true
}
func ValidateMCPReadinessPagerDutyRequest(r SetMCPReadinessPagerDutyRequest) error {
	if r.ExpectedVersion == nil || *r.ExpectedVersion < 0 || *r.ExpectedVersion == math.MaxInt64 || r.Enabled == nil {
		return errors.New("supply expected_version and enabled")
	}
	if *r.Enabled && !ValidMCPReadinessPagerDutyKey(r.IntegrationKey) || !*r.Enabled && r.IntegrationKey != "" {
		return errors.New("enable with a 32-character integration key; omit the key when disabling")
	}
	if _, ok := MCPReadinessPagerDutyEndpointForRegion(r.Region); !ok || !*r.Enabled && r.Region != "" {
		return errors.New("select us or eu when enabling; omit region when disabling")
	}
	return nil
}
func (c *Client) GetMCPReadinessPagerDuty(ctx context.Context, app, rule string) (MCPReadinessPagerDutyPolicy, error) {
	var out MCPReadinessPagerDutyPolicy
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-pagerduty", nil, &out)
	return out, err
}
func (c *Client) SetMCPReadinessPagerDuty(ctx context.Context, app, rule string, r SetMCPReadinessPagerDutyRequest) (MCPReadinessPagerDutyPolicy, error) {
	var out MCPReadinessPagerDutyPolicy
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-pagerduty", r, &out)
	return out, err
}
