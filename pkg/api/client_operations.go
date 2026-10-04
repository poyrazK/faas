package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) PutOperationDefinition(ctx context.Context, slug, deployment, name string, spec OperationDefinitionSpec) (OperationDefinitionResponse, error) {
	var out OperationDefinitionResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-definitions/" + url.PathEscape(name)
	err := c.do(ctx, http.MethodPut, path, spec, &out)
	return out, err
}

func (c *Client) GetOperation(ctx context.Context, slug, id string) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodGet, operationAppPath(slug, id), nil, &out)
	return out, err
}

func (c *Client) CancelOperation(ctx context.Context, slug, id string, req OperationCancellationRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/cancel", req, &out)
	return out, err
}

func (c *Client) StartPlatformTenantSelfOperation(ctx context.Context, req OperationStartRequest, key string) (OperationAcceptedResponse, error) {
	var out OperationAcceptedResponse
	if key == "" {
		return out, fmt.Errorf("operation submission requires a stable idempotency key")
	}
	err := c.doWithIdempotencyKey(ctx, http.MethodPost, "/v1/platform-tenant-self/customer-operations", req, &out, key)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperation(ctx context.Context, id string) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodGet, operationSelfPath(id), nil, &out)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperationEvents(ctx context.Context, id string, after int64) (OperationEventsResponse, error) {
	var out OperationEventsResponse
	err := c.do(ctx, http.MethodGet, operationSelfPath(id)+"/events?after="+strconv.FormatInt(after, 10), nil, &out)
	return out, err
}

func (c *Client) CancelPlatformTenantSelfOperation(ctx context.Context, id string, req OperationCancellationRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodPost, operationSelfPath(id)+"/cancel", req, &out)
	return out, err
}

func operationAppPath(slug, id string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/operations/" + url.PathEscape(id)
}

func operationSelfPath(id string) string {
	return "/v1/platform-tenant-self/customer-operations/" + url.PathEscape(id)
}
