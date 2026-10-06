package api

import (
	"context"
	"net/url"
)

type OutboundBindingProbePolicy struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	ExpectedStatus int    `json:"expected_status"`
}

func (c *Client) GetOutboundBindingProbePolicy(ctx context.Context, integration string) (OutboundBindingProbePolicy, error) {
	var out OutboundBindingProbePolicy
	err := c.do(ctx, "GET", "/v1/outbound/integrations/"+url.PathEscape(integration)+"/probe-policy", nil, &out)
	return out, err
}
func (c *Client) SetOutboundBindingProbePolicy(ctx context.Context, integration string, policy OutboundBindingProbePolicy) (OutboundBindingProbePolicy, error) {
	var out OutboundBindingProbePolicy
	err := c.do(ctx, "PUT", "/v1/outbound/integrations/"+url.PathEscape(integration)+"/probe-policy", policy, &out)
	return out, err
}
func (c *Client) DeleteOutboundBindingProbePolicy(ctx context.Context, integration string) error {
	return c.do(ctx, "DELETE", "/v1/outbound/integrations/"+url.PathEscape(integration)+"/probe-policy", nil, nil)
}
