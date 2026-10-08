package api

import (
	"context"
	"net/url"
	"strconv"
)

func (c *Client) ListProfileInvestigations(ctx context.Context, slug string) (ListProfileInvestigationsResponse, error) {
	var out ListProfileInvestigationsResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations", nil, &out)
	return out, err
}

func (c *Client) GetProfileInvestigation(ctx context.Context, slug, id string) (ProfileInvestigationResponse, error) {
	var out ProfileInvestigationResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) CreateProfileInvestigation(ctx context.Context, slug string, req SaveProfileInvestigationRequest) (ProfileInvestigationResponse, error) {
	var out ProfileInvestigationResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations", req, &out)
	return out, err
}

func (c *Client) UpdateProfileInvestigation(ctx context.Context, slug, id string, req SaveProfileInvestigationRequest) (ProfileInvestigationResponse, error) {
	var out ProfileInvestigationResponse
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations/"+url.PathEscape(id), req, &out)
	return out, err
}

func (c *Client) DeleteProfileInvestigation(ctx context.Context, slug, id string, revision int64) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations/"+url.PathEscape(id)+"?expected_revision="+strconv.FormatInt(revision, 10), nil, nil)
}

func (c *Client) CheckProfileRegression(ctx context.Context, slug, id string, req CheckProfileRegressionRequest) (ProfileInvestigationResponse, error) {
	var out ProfileInvestigationResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/profiles/investigations/"+url.PathEscape(id)+"/check", req, &out)
	return out, err
}
