package api

import (
	"context"
	"net/url"
)

// AIDeploymentUsage is one deployment's AI gateway calls over the last
// OutboundAIDeploymentStatsRetentionDays days (ADR 20261022153155). The
// first and last call times are UTC minutes.
type AIDeploymentUsage struct {
	DeploymentID   string `json:"deployment_id"`
	FirstCallAt    string `json:"first_call_at"`
	LastCallAt     string `json:"last_call_at"`
	Calls          int64  `json:"calls"`
	ErrorCalls     int64  `json:"error_calls"`
	FlaggedCalls   int64  `json:"flagged_calls"`
	CostMillicents int64  `json:"cost_millicents"`
	InputTokens    int64  `json:"input_tokens"`
	OutputTokens   int64  `json:"output_tokens"`
	// ErrorRate and FlaggedRate are fractions of calls, and
	// CostPerCallMillicents the cost over calls, rounded. MeanTTFBMS is the
	// mean time to first byte, omitted when no call had one.
	ErrorRate             float64 `json:"error_rate"`
	FlaggedRate           float64 `json:"flagged_rate"`
	CostPerCallMillicents int64   `json:"cost_per_call_millicents"`
	MeanTTFBMS            *int64  `json:"mean_ttfb_ms,omitempty"`
}

// AIDeploymentUsageList lists an app's deployments with AI calls, latest
// call first.
type AIDeploymentUsageList struct {
	Items []AIDeploymentUsage `json:"items"`
}

// ListAIDeployments lists an app's AI calls by deployment (ADR
// 20261022153155).
func (c *Client) ListAIDeployments(ctx context.Context, slug string) (AIDeploymentUsageList, error) {
	var out AIDeploymentUsageList
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/ai-deployments", nil, &out)
}
