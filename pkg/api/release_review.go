package api

import (
	"context"
	"net/url"
)

func (c *Client) GetAppReleaseReview(ctx context.Context, slug string) (AppReleaseReview, error) {
	var out AppReleaseReview
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/release-review", nil, &out)
}

func (c *Client) GetAppReleaseReviewInEnvironment(ctx context.Context, slug, environment string) (AppReleaseReview, error) {
	var out AppReleaseReview
	if !ValidProjectEnvironmentSlug(environment) {
		return out, ErrValidation("invalid project environment")
	}
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/release-review?environment="+url.QueryEscape(environment), nil, &out)
}

func (c *Client) GetAppHealthInEnvironment(ctx context.Context, slug, environment string) (AppHealthResponse, error) {
	var out AppHealthResponse
	if !ValidProjectEnvironmentSlug(environment) {
		return out, ErrValidation("invalid project environment")
	}
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/health?environment="+url.QueryEscape(environment), nil, &out)
}

func (c *Client) PatchDeploymentTrafficReviewed(ctx context.Context, id, serving string, review AppReleaseReview) (DeploymentResponse, error) {
	var out DeploymentResponse
	return out, c.doWithHeaders(ctx, "PATCH", "/v1/deployments/"+url.PathEscape(id)+"/traffic", UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &serving, ExpectedReview: &review}, &out, deploymentPromotionHeaders(ctx))
}
