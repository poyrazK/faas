package api

import (
	"context"
	"errors"
	"net/url"
)

func (c *Client) InspectDurableEntity(ctx context.Context, slug string, request DurableEntityInspectRequest) (DurableEntityInspectResponse, error) {
	var out DurableEntityInspectResponse
	if request.Namespace == "" || request.Key == "" {
		return out, errors.New("durable entity inspection requires namespace and key")
	}
	query := url.Values{"namespace": {request.Namespace}, "key": {request.Key}}
	if request.Environment != "" {
		query.Set("environment", request.Environment)
	}
	if request.PlatformTenantID != "" {
		query.Set("platform_tenant_id", request.PlatformTenantID)
	}
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/entities/inspect?"+query.Encode(), nil, &out)
}
