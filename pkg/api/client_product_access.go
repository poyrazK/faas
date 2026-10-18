package api

import (
	"context"
	"net/http"
)

func (c *Client) GetProductAccess(ctx context.Context, slug string) (ProductAccessConfiguration, error) {
	var out ProductAccessConfiguration
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/access", nil, &out)
	return out, err
}

func (c *Client) PreviewProductAccess(ctx context.Context, slug string, policy ProductAccessPolicy) (ProductAccessChangePreview, error) {
	var out ProductAccessChangePreview
	err := c.do(ctx, http.MethodPost, productPath(slug)+"/access/preview", policy, &out)
	return out, err
}

func (c *Client) ApplyProductAccess(ctx context.Context, slug string, change ProductAccessChangeRequest) (ProductAccessConfiguration, error) {
	var out ProductAccessConfiguration
	err := c.do(ctx, http.MethodPut, productPath(slug)+"/access", change, &out)
	return out, err
}
