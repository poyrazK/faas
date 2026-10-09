package api

import (
	"context"
	"net/url"
	"strconv"
)

func (c *Client) GetProfileDeploymentPolicy(ctx context.Context, slug string) (ProfileDeploymentPolicy, error) {
	var out ProfileDeploymentPolicy
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/deployment-policy", nil, &out)
	return out, err
}

func (c *Client) SaveProfileDeploymentPolicy(ctx context.Context, slug string, req SaveProfileDeploymentPolicyRequest) (ProfileDeploymentPolicy, error) {
	var out ProfileDeploymentPolicy
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/profiles/deployment-policy", req, &out)
	return out, err
}

func (c *Client) ListProfileDeploymentChecks(ctx context.Context, slug string) (ListProfileDeploymentChecksResponse, error) {
	var out ListProfileDeploymentChecksResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/deployment-checks", nil, &out)
	return out, err
}

func (c *Client) GetProfileDeploymentCheck(ctx context.Context, slug, id string) (ProfileDeploymentCheck, error) {
	var out ProfileDeploymentCheck
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/deployment-checks/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) ListProfileCanaryChecks(ctx context.Context, slug, deploymentID string, limit int, before string) (ProfileCanaryHistoryPage, error) {
	var out ProfileCanaryHistoryPage
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if before != "" {
		query.Set("before", before)
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/profiles/canary-checks/" + url.PathEscape(deploymentID)
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}

func (c *Client) ListProfilePeriodicMonitors(ctx context.Context, slug string) (ListProfilePeriodicMonitorsResponse, error) {
	var out ListProfilePeriodicMonitorsResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/periodic-monitors", nil, &out)
	return out, err
}
