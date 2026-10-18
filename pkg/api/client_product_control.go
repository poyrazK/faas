package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) StartProductFeature(ctx context.Context, req ProductStartRequest, key string) (OperationAcceptedResponse, error) {
	var out OperationAcceptedResponse
	if key == "" || len(key) > OperationIdempotencyKeyMaxBytes || strings.ContainsAny(key, "\r\n\x00") {
		return out, fmt.Errorf("product submission requires a stable idempotency key")
	}
	err := c.doWithIdempotencyKey(ctx, http.MethodPost, "/v1/platform-tenant-self/product/start", req, &out, key)
	return out, err
}

func productPath(slug string) string { return "/v1/apps/" + url.PathEscape(slug) + "/product" }

func (c *Client) GetProductDoctor(ctx context.Context, slug, tenant string) (ProductDoctorReport, error) {
	var out ProductDoctorReport
	q := url.Values{}
	if tenant != "" {
		q.Set("tenant_id", tenant)
	}
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/doctor?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetProductCatalog(ctx context.Context, slug, revision string) (ProductCatalog, error) {
	var out ProductCatalog
	err := c.do(ctx, http.MethodGet, productPath(slug)+"?revision="+url.QueryEscape(revision), nil, &out)
	return out, err
}

func (c *Client) PutProductCatalog(ctx context.Context, slug string, spec ProductCatalogSpec) (ProductCatalog, error) {
	var out ProductCatalog
	err := c.do(ctx, http.MethodPut, productPath(slug), spec, &out)
	return out, err
}

func (c *Client) SetProductCustomer(ctx context.Context, slug string, customer ProductCustomer) (ProductCustomer, error) {
	var out ProductCustomer
	req := struct {
		AuthOrganizationID string `json:"auth_organization_id"`
		Plan               string `json:"plan"`
		Revision           string `json:"revision"`
		Suspended          bool   `json:"suspended"`
	}{customer.AuthOrganizationID, customer.Plan, customer.Revision, customer.Suspended}
	err := c.do(ctx, http.MethodPut, productPath(slug)+"/customers/"+url.PathEscape(customer.TenantID), req, &out)
	return out, err
}

func (c *Client) ListProductCustomers(ctx context.Context, slug, cursor string) (ProductCustomersResponse, error) {
	var out ProductCustomersResponse
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/customers?cursor="+url.QueryEscape(cursor), nil, &out)
	return out, err
}

func (c *Client) GetProductUsage(ctx context.Context, slug, tenant, period string) (ProductUsage, error) {
	var out ProductUsage
	query := url.Values{"tenant_id": {tenant}, "period": {period}}
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/usage?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetProductPortal(ctx context.Context, slug, period string) (ProductPortal, error) {
	var out ProductPortal
	query := url.Values{"app": {slug}, "period": {period}}
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/product?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) ExplainProductOperation(ctx context.Context, slug, id string, after int) (ProductOperationExplanation, error) {
	var out ProductOperationExplanation
	if after < 0 || after > math.MaxInt32 {
		return out, fmt.Errorf("execution generation cursor must fit a nonnegative 32-bit integer")
	}
	path := productPath(slug) + "/operations/" + url.PathEscape(id) + "/explain?after=" + strconv.Itoa(after)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
