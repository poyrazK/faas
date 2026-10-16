package api

import (
	"context"
	"net/url"
	"time"
)

// EnvironmentCacheConvergence is fresh, session-bound gateway evidence for
// the selected desired revision, never a claim about future serving behavior.
type EnvironmentCacheConvergence struct {
	AppID                string    `json:"app_id"`
	ProjectID            string    `json:"project_id"`
	EnvironmentID        string    `json:"environment_id"`
	Scope                string    `json:"scope"`
	Revision             int64     `json:"revision"`
	Status               string    `json:"status"`
	CheckedAt            time.Time `json:"checked_at"`
	ServingGateways      int64     `json:"serving_gateways"`
	AcknowledgedGateways int64     `json:"acknowledged_gateways"`
	PendingGateways      int64     `json:"pending_gateways"`
	StaleGateways        int64     `json:"stale_gateways"`
}

func (c *Client) GetEnvironmentCacheConvergence(ctx context.Context, slug, environment string) (EnvironmentCacheConvergence, error) {
	var out EnvironmentCacheConvergence
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/cache-policy/convergence?environment="+url.QueryEscape(environment), nil, &out)
	return out, err
}
