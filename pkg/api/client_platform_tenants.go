package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

func (c *Client) ListPlatformTenants(ctx context.Context, limit, offset int) (PlatformTenantListResponse, error) {
	var out PlatformTenantListResponse
	path := "/v1/account/platform-tenants"
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) CreatePlatformTenant(ctx context.Context, req CreatePlatformTenantRequest) (PlatformTenantResponse, error) {
	var out PlatformTenantResponse
	return out, c.do(ctx, "POST", "/v1/account/platform-tenants", req, &out)
}

func (c *Client) ApplyPlatformTenant(ctx context.Context, req ApplyPlatformTenantRequest) (ApplyPlatformTenantResponse, error) {
	var out ApplyPlatformTenantResponse
	return out, c.do(ctx, "POST", "/v1/account/platform-tenants/apply", req, &out)
}

func (c *Client) GetPlatformTenant(ctx context.Context, id string) (PlatformTenantDetailResponse, error) {
	var out PlatformTenantDetailResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+url.PathEscape(id), nil, &out)
}

func (c *Client) GetPlatformTenantActivation(ctx context.Context, id string) (PlatformTenantActivationResponse, error) {
	var out PlatformTenantActivationResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+url.PathEscape(id)+"/activation", nil, &out)
}

func (c *Client) SetPlatformTenantStatus(ctx context.Context, id string, req SetPlatformTenantStatusRequest) (PlatformTenantResponse, error) {
	var out PlatformTenantResponse
	return out, c.do(ctx, "PATCH", "/v1/account/platform-tenants/"+url.PathEscape(id), req, &out)
}

func (c *Client) LinkPlatformTenantConsumer(ctx context.Context, id string, req LinkPlatformTenantConsumerRequest) (APIConsumerResponse, error) {
	var out APIConsumerResponse
	return out, c.do(ctx, "POST", "/v1/account/platform-tenants/"+url.PathEscape(id)+"/consumers", req, &out)
}

func (c *Client) LinkPlatformTenantSurface(ctx context.Context, id string, req LinkPlatformTenantSurfaceRequest) (PlatformTenantSurfaceResponse, error) {
	var out PlatformTenantSurfaceResponse
	return out, c.do(ctx, "POST", "/v1/account/platform-tenants/"+url.PathEscape(id)+"/surfaces", req, &out)
}

func (c *Client) GetPlatformTenantUsage(ctx context.Context, id string, opts APIConsumerUsageOptions) (PlatformTenantUsageResponse, error) {
	var out PlatformTenantUsageResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(id) + "/usage"
	q := url.Values{}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Until != "" {
		q.Set("until", opts.Until)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func platformTenantStatementsPath(tenantID string) string {
	return "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/usage-statements"
}

func (c *Client) CreatePlatformTenantStatement(ctx context.Context, tenantID string, req CreateAPIConsumerUsageStatementRequest) (PlatformTenantStatementResponse, error) {
	var out PlatformTenantStatementResponse
	return out, c.do(ctx, "POST", platformTenantStatementsPath(tenantID), req, &out)
}

func (c *Client) ListPlatformTenantStatements(ctx context.Context, tenantID string, start, end time.Time) (PlatformTenantStatementListResponse, error) {
	var out PlatformTenantStatementListResponse
	q := url.Values{"period_start": {start.UTC().Format(time.RFC3339)}, "period_end": {end.UTC().Format(time.RFC3339)}}
	return out, c.do(ctx, "GET", platformTenantStatementsPath(tenantID)+"?"+q.Encode(), nil, &out)
}

func (c *Client) GetPlatformTenantStatement(ctx context.Context, tenantID, statementID string) (PlatformTenantStatementResponse, error) {
	var out PlatformTenantStatementResponse
	return out, c.do(ctx, "GET", platformTenantStatementsPath(tenantID)+"/"+url.PathEscape(statementID), nil, &out)
}

func (c *Client) FinalizePlatformTenantStatement(ctx context.Context, tenantID, statementID string) (PlatformTenantStatementResponse, error) {
	var out PlatformTenantStatementResponse
	return out, c.do(ctx, "POST", platformTenantStatementsPath(tenantID)+"/"+url.PathEscape(statementID)+"/finalize", struct{}{}, &out)
}

func (c *Client) ClaimPlatformTenantStatement(ctx context.Context, tenantID, statementID string, req ClaimAPIConsumerUsageStatementRequest) (PlatformTenantStatementHandoffResponse, error) {
	var out PlatformTenantStatementHandoffResponse
	return out, c.do(ctx, "POST", platformTenantStatementsPath(tenantID)+"/"+url.PathEscape(statementID)+"/handoff", req, &out)
}

func (c *Client) GetPlatformTenantStatementHandoff(ctx context.Context, tenantID, statementID string) (PlatformTenantStatementHandoffResponse, error) {
	var out PlatformTenantStatementHandoffResponse
	return out, c.do(ctx, "GET", platformTenantStatementsPath(tenantID)+"/"+url.PathEscape(statementID)+"/handoff", nil, &out)
}
