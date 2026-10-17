package api

import (
	"context"
	"net/http"
)

func (c *Client) GetProductTeamPolicy(ctx context.Context, slug string) (ProductTeamPolicyConfiguration, error) {
	var out ProductTeamPolicyConfiguration
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/team-policy", nil, &out)
	return out, err
}

func (c *Client) PreviewProductTeamPolicy(ctx context.Context, slug string, policy ProductTeamPolicy) (ProductTeamPolicyPreview, error) {
	var out ProductTeamPolicyPreview
	err := c.do(ctx, http.MethodPost, productPath(slug)+"/team-policy/preview", policy, &out)
	return out, err
}

func (c *Client) ApplyProductTeamPolicy(ctx context.Context, slug string, change ProductTeamPolicyChange) (ProductTeamPolicyConfiguration, error) {
	var out ProductTeamPolicyConfiguration
	err := c.do(ctx, http.MethodPut, productPath(slug)+"/team-policy", change, &out)
	return out, err
}
