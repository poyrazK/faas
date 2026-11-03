package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/financial"
)

// An organization that owns its resources pays for them, so it holds the
// costs and budgets for that usage (ADR-20261031211500). These mirror the
// account's /v1/billing/costs and /v1/billing/budgets methods.

func orgBillingPath(slug, suffix string) string {
	return "/v1/orgs/" + url.PathEscape(slug) + "/billing" + suffix
}

// GetOrgFinancialCosts reads the organization's attributable usage costs for
// a UTC month ("YYYY-MM"), or the current month when month is empty.
func (c *Client) GetOrgFinancialCosts(ctx context.Context, slug, month string) (FinancialCostsResponse, error) {
	var out FinancialCostsResponse
	path := orgBillingPath(slug, "/costs")
	if month != "" {
		path += "?month=" + url.QueryEscape(month)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) PreviewOrgFinancialBudget(ctx context.Context, slug string, spec financial.BudgetSpec) (FinancialBudgetPreviewResponse, error) {
	var out FinancialBudgetPreviewResponse
	err := c.do(ctx, http.MethodPost, orgBillingPath(slug, "/budgets/preview"), FinancialBudgetPreviewRequest{Spec: spec}, &out)
	return out, err
}

func (c *Client) ListOrgFinancialBudgets(ctx context.Context, slug string) (FinancialBudgetListResponse, error) {
	var out FinancialBudgetListResponse
	err := c.do(ctx, http.MethodGet, orgBillingPath(slug, "/budgets"), nil, &out)
	return out, err
}

func (c *Client) GetOrgFinancialBudget(ctx context.Context, slug, id string) (FinancialBudgetResponse, error) {
	var out FinancialBudgetResponse
	err := c.do(ctx, http.MethodGet, orgBillingPath(slug, "/budgets/"+url.PathEscape(id)), nil, &out)
	return out, err
}

func (c *Client) CreateOrgFinancialBudget(ctx context.Context, slug string, req CreateFinancialBudgetRequest) (FinancialBudgetResponse, error) {
	var out FinancialBudgetResponse
	err := c.do(ctx, http.MethodPost, orgBillingPath(slug, "/budgets"), req, &out)
	return out, err
}

func (c *Client) UpdateOrgFinancialBudget(ctx context.Context, slug, id string, req UpdateFinancialBudgetRequest) (FinancialBudgetResponse, error) {
	var out FinancialBudgetResponse
	err := c.do(ctx, http.MethodPut, orgBillingPath(slug, "/budgets/"+url.PathEscape(id)), req, &out)
	return out, err
}

func (c *Client) DeleteOrgFinancialBudget(ctx context.Context, slug, id string, expectedRevision int64) (FinancialBudgetResponse, error) {
	var out FinancialBudgetResponse
	err := c.do(ctx, http.MethodDelete, orgBillingPath(slug, "/budgets/"+url.PathEscape(id)), DeleteFinancialBudgetRequest{ExpectedRevision: expectedRevision}, &out)
	return out, err
}

func (c *Client) ListOrgFinancialBudgetRevisions(ctx context.Context, slug, id string, after int64, limit int) (FinancialBudgetHistoryResponse, error) {
	var out FinancialBudgetHistoryResponse
	q := url.Values{}
	q.Set("after_revision", strconv.FormatInt(after, 10))
	q.Set("limit", strconv.Itoa(limit))
	err := c.do(ctx, http.MethodGet, orgBillingPath(slug, "/budgets/"+url.PathEscape(id)+"/revisions?"+q.Encode()), nil, &out)
	return out, err
}
