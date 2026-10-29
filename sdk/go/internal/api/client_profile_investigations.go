package api

import (
	"context"
	"net/url"
	"strconv"
)

func (c *PreviewClient) ListProfileInvestigations(ctx context.Context, slug string) (ListProfileInvestigationsResponse, error) {
	var out ListProfileInvestigationsResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations", nil, &out)
	return out, err
}

func (c *PreviewClient) GetProfileInvestigation(ctx context.Context, slug, id string) (ProfileInvestigationResponse, error) {
	var out ProfileInvestigationResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *PreviewClient) CreateProfileInvestigation(ctx context.Context, slug string, req SaveProfileInvestigationRequest) (ProfileInvestigationResponse, error) {
	var out ProfileInvestigationResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations", req, &out)
	return out, err
}

func (c *PreviewClient) UpdateProfileInvestigation(ctx context.Context, slug, id string, req SaveProfileInvestigationRequest) (ProfileInvestigationResponse, error) {
	var out ProfileInvestigationResponse
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations/"+url.PathEscape(id), req, &out)
	return out, err
}

func (c *PreviewClient) DeleteProfileInvestigation(ctx context.Context, slug, id string, revision int64) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations/"+url.PathEscape(id)+"?expected_revision="+strconv.FormatInt(revision, 10), nil, nil)
}

func (c *PreviewClient) CheckProfileRegression(ctx context.Context, slug, id string, req CheckProfileRegressionRequest) (ProfileInvestigationResponse, error) {
	var out ProfileInvestigationResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations/"+url.PathEscape(id)+"/check", req, &out)
	return out, err
}
