package api

import (
	"context"
	"net/http"
	"net/url"
)

func secretReferencePath(app, environment, key string) string {
	path := "/v1/apps/" + url.PathEscape(app) + "/secret-references"
	if key != "" {
		path += "/" + url.PathEscape(key)
	}
	return path + "?" + url.Values{"environment": {environment}}.Encode()
}

func (c *Client) ListAppSecretReferences(ctx context.Context, app, environment string) (AppSecretReferenceListResponse, error) {
	var out AppSecretReferenceListResponse
	err := c.do(ctx, http.MethodGet, secretReferencePath(app, environment, ""), nil, &out)
	return out, err
}

func (c *Client) SetAppSecretReference(ctx context.Context, app, environment, key string, request PutAppSecretReferenceRequest) (AppSecretReferenceResponse, error) {
	var out AppSecretReferenceResponse
	err := c.do(ctx, http.MethodPut, secretReferencePath(app, environment, key), request, &out)
	return out, err
}

func (c *Client) DeleteAppSecretReference(ctx context.Context, app, environment, key string) error {
	return c.do(ctx, http.MethodDelete, secretReferencePath(app, environment, key), nil, nil)
}
