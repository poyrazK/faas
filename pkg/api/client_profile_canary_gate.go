package api

import (
	"context"
	"net/url"
)

func (c *Client) GetProfileCanaryGate(ctx context.Context, id string) (ProfileCanaryGateDecision, error) {
	var out ProfileCanaryGateDecision
	err := c.do(ctx, "GET", "/v1/deployments/"+url.PathEscape(id)+"/canary/profile-gate", nil, &out)
	return out, err
}

func (c *Client) AdvanceCanaryWithProfileOverride(ctx context.Context, id string, expectedStep int, override ProfileGateOverride) (CanaryAdvanceResponse, error) {
	var out CanaryAdvanceResponse
	err := c.do(ctx, "POST", "/v1/deployments/"+url.PathEscape(id)+"/canary/advance", AdvanceCanaryRequest{ExpectedStep: expectedStep, ProfileGateOverride: &override}, &out)
	return out, err
}
