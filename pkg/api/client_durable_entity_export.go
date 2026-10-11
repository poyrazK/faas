package api

import (
	"context"
	"errors"
	"net/url"
)

func (c *Client) ExportDurableEntity(ctx context.Context, slug string, request DurableEntityInspectRequest) (DurableEntityStateExport, error) {
	var out DurableEntityStateExport
	if request.Namespace == "" || request.Key == "" {
		return out, errors.New("entity export requires namespace and key")
	}
	query := url.Values{"namespace": {request.Namespace}, "key": {request.Key}}
	if request.Environment != "" {
		query.Set("environment", request.Environment)
	}
	if request.PlatformTenantID != "" {
		query.Set("platform_tenant_id", request.PlatformTenantID)
	}
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/entities/export?"+query.Encode(), nil, &out)
}

// RestoreDurableEntity requires an explicit stable ID. Retry uncertain outcomes
// with the identical request, including the original expected version.
func (c *Client) RestoreDurableEntity(ctx context.Context, slug string, request DurableEntityRestoreRequest) (DurableEntityRestoreResponse, error) {
	var out DurableEntityRestoreResponse
	if request.Namespace == "" || request.Key == "" || request.RequestID == "" || request.ExpectedVersion == 0 {
		return out, errors.New("entity restore requires selectors, request_id and expected_version")
	}
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/entities/restore", request, &out)
}
