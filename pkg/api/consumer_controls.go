package api

import "context"

// CodeConsumerInvoiceOverdue is the gateway's 402 for a consumer blocked by
// an overdue invoice (ADR-1315).
const CodeConsumerInvoiceOverdue = "consumer_invoice_overdue"

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
