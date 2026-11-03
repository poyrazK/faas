package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) GetOrgBillingStatus(ctx context.Context, slug string) (BillingStatusResponse, error) {
	var out BillingStatusResponse
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(slug)+"/billing/status", nil, &out)
	return out, err
}

func (c *Client) GetOrgUsage(ctx context.Context, slug, month string) ([]UsageResponse, error) {
	var out []UsageResponse
	query := url.Values{}
	if month != "" {
		query.Set("month", month)
	}
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(slug)+"/usage?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetOrgBillingPortal(ctx context.Context, slug string) (BillingPortalResponse, error) {
	var out BillingPortalResponse
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(slug)+"/billing/portal", nil, &out)
	return out, err
}

func (c *Client) ListOrgInvoices(ctx context.Context, slug, month, before string, limit int) (InvoiceListResponse, error) {
	query := url.Values{}
	if month != "" {
		query.Set("month", month)
	}
	if before != "" {
		query.Set("before", before)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var out InvoiceListResponse
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(slug)+"/invoices?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) ChangeOrgPlan(ctx context.Context, slug string, plan Plan) (OrgPlanResponse, error) {
	var out OrgPlanResponse
	err := c.do(ctx, http.MethodPatch, "/v1/orgs/"+url.PathEscape(slug)+"/plan", map[string]Plan{"plan": plan}, &out)
	return out, err
}

// GetOrgOverageCap reads the organization's monthly spend cap
// (ADR-20261030093512). A nil cap means no ceiling; zero allows no overage.
func (c *Client) GetOrgOverageCap(ctx context.Context, slug string) (AccountOverageCapResponse, error) {
	var out AccountOverageCapResponse
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(slug)+"/overage-cap", nil, &out)
	return out, err
}

// SetOrgOverageCap sets the organization's monthly spend cap in cents, or
// clears it with nil.
func (c *Client) SetOrgOverageCap(ctx context.Context, slug string, overageCapCents *int64) (AccountOverageCapResponse, error) {
	var out AccountOverageCapResponse
	err := c.do(ctx, http.MethodPost, "/v1/orgs/"+url.PathEscape(slug)+"/overage-cap", map[string]any{"overage_cap_cents": overageCapCents}, &out)
	return out, err
}
