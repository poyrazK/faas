package api

import (
	"context"
	"net/http"
)

func (c *Client) GetProductPlanPolicy(ctx context.Context, slug string) (ProductPlanPolicyConfiguration, error) {
	var out ProductPlanPolicyConfiguration
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/plan-policy", nil, &out)
	return out, err
}

func (c *Client) PreviewProductPlanPolicy(ctx context.Context, slug string, policy ProductPlanPolicy) (ProductPlanPolicyPreview, error) {
	var out ProductPlanPolicyPreview
	err := c.do(ctx, http.MethodPost, productPath(slug)+"/plan-policy/preview", policy, &out)
	return out, err
}

func (c *Client) ApplyProductPlanPolicy(ctx context.Context, slug string, change ProductPlanPolicyChange) (ProductPlanPolicyConfiguration, error) {
	var out ProductPlanPolicyConfiguration
	err := c.do(ctx, http.MethodPut, productPath(slug)+"/plan-policy", change, &out)
	return out, err
}
