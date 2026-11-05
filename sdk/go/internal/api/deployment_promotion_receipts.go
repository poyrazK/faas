package api

import (
	"context"
	"net/url"
	"time"
)

// DeploymentPromotionReceipt is immutable historical traffic acceptance.
type DeploymentPromotionReceipt struct {
	AttemptID                   string    `json:"attempt_id"`
	RequestHash                 string    `json:"request_hash"`
	AppID                       string    `json:"app_id"`
	DeploymentID                string    `json:"deployment_id"`
	ExpectedServingDeploymentID string    `json:"expected_serving_deployment_id"`
	Scope                       string    `json:"scope"`
	ProjectID                   string    `json:"project_id,omitempty"`
	EnvironmentID               string    `json:"environment_id,omitempty"`
	AcceptedAt                  time.Time `json:"accepted_at"`
}

func (c *Client) GetDeploymentPromotionReceipt(ctx context.Context, slug, attemptID string) (DeploymentPromotionReceipt, error) {
	var out DeploymentPromotionReceipt
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/deployment-promotions/"+url.PathEscape(attemptID), nil, &out)
}
