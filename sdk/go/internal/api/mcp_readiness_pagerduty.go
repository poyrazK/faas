package api

import (
	"context"
	"net/url"
)

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

func (c *PreviewClient) GetMCPReadinessPagerDuty(ctx context.Context, app, rule string) (MCPReadinessPagerDutyPolicy, error) {
	var out MCPReadinessPagerDutyPolicy
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-pagerduty", nil, &out)
	return out, err
}
func (c *PreviewClient) SetMCPReadinessPagerDuty(ctx context.Context, app, rule string, r SetMCPReadinessPagerDutyRequest) (MCPReadinessPagerDutyPolicy, error) {
	var out MCPReadinessPagerDutyPolicy
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-pagerduty", r, &out)
	return out, err
}
