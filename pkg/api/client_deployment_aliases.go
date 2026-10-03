package api

import (
	"context"
	"net/http"
	"net/url"
)

// ListDeploymentAliases returns the named revision aliases attached to an app.
func (c *Client) ListDeploymentAliases(ctx context.Context, slug string) (DeploymentAliasListResponse, error) {
	var out DeploymentAliasListResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/deployment-aliases", nil, &out)
	return out, err
}

// SetDeploymentAlias points name at one exact deployment belonging to slug.
func (c *Client) SetDeploymentAlias(ctx context.Context, slug, name string, req SetDeploymentAliasRequest) (DeploymentAliasResponse, error) {
	var out DeploymentAliasResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployment-aliases/" + url.PathEscape(name)
	err := c.do(ctx, http.MethodPut, path, req, &out)
	return out, err
}

// DeleteDeploymentAlias removes the named mapping without deleting its target.
func (c *Client) DeleteDeploymentAlias(ctx context.Context, slug, name string) error {
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployment-aliases/" + url.PathEscape(name)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
