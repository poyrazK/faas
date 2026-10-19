package api

import "context"

// CodeConsumerInvoiceOverdue is the gateway's 402 for a consumer blocked by
// an overdue invoice (ADR-1315).
const CodeConsumerInvoiceOverdue = "consumer_invoice_overdue"

// CodeTenantInvoiceOverdue is the gateway's 402 for a platform tenant
// blocked by an overdue invoice (ADR-1324).
const CodeTenantInvoiceOverdue = "tenant_invoice_overdue"

// GetAPIConsumerTrial reads a consumer's free trial (ADR-1314).
func (c *Client) GetAPIConsumerTrial(ctx context.Context, slug, consumerID string) (APIConsumerTrialResponse, error) {
	var out APIConsumerTrialResponse
	return out, c.do(ctx, "GET", consumerPath(slug, consumerID)+"/trial", nil, &out)
}

// PutAPIConsumerTrial starts a consumer's trial or moves the end of a
// running one.
func (c *Client) PutAPIConsumerTrial(ctx context.Context, slug, consumerID string, req PutAPIConsumerTrialRequest) (APIConsumerTrialResponse, error) {
	var out APIConsumerTrialResponse
	return out, c.do(ctx, "PUT", consumerPath(slug, consumerID)+"/trial", req, &out)
}

// EndAPIConsumerTrial ends a consumer's running trial at the next minute.
func (c *Client) EndAPIConsumerTrial(ctx context.Context, slug, consumerID string) error {
	return c.do(ctx, "DELETE", consumerPath(slug, consumerID)+"/trial", nil, nil)
}

// PutAPIConsumerBillingPolicy sets an app's overdue rule (ADR-1315).
func (c *Client) PutAPIConsumerBillingPolicy(ctx context.Context, slug string, req PutAPIConsumerBillingPolicyRequest) (APIConsumerBillingPolicyResponse, error) {
	var out APIConsumerBillingPolicyResponse
	return out, c.do(ctx, "PUT", "/v1/apps/"+slug+"/consumer-billing-policy", req, &out)
}

// GetAPIConsumerBillingPolicy reads an app's overdue rule.
func (c *Client) GetAPIConsumerBillingPolicy(ctx context.Context, slug string) (APIConsumerBillingPolicyResponse, error) {
	var out APIConsumerBillingPolicyResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/consumer-billing-policy", nil, &out)
}

// DeleteAPIConsumerBillingPolicy removes an app's overdue rule; its holds
// lift at the next check.
func (c *Client) DeleteAPIConsumerBillingPolicy(ctx context.Context, slug string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/consumer-billing-policy", nil, nil)
}

// ListAPIConsumerOverdueHolds lists an app's consumers blocked by overdue
// invoices.
func (c *Client) ListAPIConsumerOverdueHolds(ctx context.Context, slug string) (APIConsumerOverdueHoldListResponse, error) {
	var out APIConsumerOverdueHoldListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/consumer-overdue-holds", nil, &out)
}

// GetPlatformTenantTrial reads a platform tenant's free trial (ADR-1322).
func (c *Client) GetPlatformTenantTrial(ctx context.Context, tenantID string) (PlatformTenantTrialResponse, error) {
	var out PlatformTenantTrialResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+tenantID+"/trial", nil, &out)
}

// PutPlatformTenantTrial starts a tenant's trial or moves the end of a
// running one (ADR-1322).
func (c *Client) PutPlatformTenantTrial(ctx context.Context, tenantID string, req PutAPIConsumerTrialRequest) (PlatformTenantTrialResponse, error) {
	var out PlatformTenantTrialResponse
	return out, c.do(ctx, "PUT", "/v1/account/platform-tenants/"+tenantID+"/trial", req, &out)
}

// EndPlatformTenantTrial ends a tenant's trial at the next minute (ADR-1322).
func (c *Client) EndPlatformTenantTrial(ctx context.Context, tenantID string) error {
	return c.do(ctx, "DELETE", "/v1/account/platform-tenants/"+tenantID+"/trial", nil, nil)
}

// PutPlatformTenantBillingPolicy sets the account's overdue rule for
// platform tenants (ADR-1324).
func (c *Client) PutPlatformTenantBillingPolicy(ctx context.Context, req PutAPIConsumerBillingPolicyRequest) (PlatformTenantBillingPolicyResponse, error) {
	var out PlatformTenantBillingPolicyResponse
	return out, c.do(ctx, "PUT", "/v1/account/platform-tenant-billing-policy", req, &out)
}

// GetPlatformTenantBillingPolicy reads the account's tenant overdue rule.
func (c *Client) GetPlatformTenantBillingPolicy(ctx context.Context) (PlatformTenantBillingPolicyResponse, error) {
	var out PlatformTenantBillingPolicyResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenant-billing-policy", nil, &out)
}

// DeletePlatformTenantBillingPolicy removes the account's tenant overdue
// rule; holds lift at the next check.
func (c *Client) DeletePlatformTenantBillingPolicy(ctx context.Context) error {
	return c.do(ctx, "DELETE", "/v1/account/platform-tenant-billing-policy", nil, nil)
}

// ListPlatformTenantOverdueHolds lists the account's tenants blocked by
// overdue invoices (ADR-1324).
func (c *Client) ListPlatformTenantOverdueHolds(ctx context.Context) (PlatformTenantOverdueHoldListResponse, error) {
	var out PlatformTenantOverdueHoldListResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenant-overdue-holds", nil, &out)
}
