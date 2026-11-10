package api

import "context"

func (c *Client) PlanDomains(ctx context.Context, manifest DomainManifest) (DomainPlan, error) {
	var out DomainPlan
	err := c.do(ctx, "POST", "/v1/domains/plan", manifest, &out)
	return out, err
}

func (c *Client) ApplyDomains(ctx context.Context, request DomainApplyRequest) (DomainApplyResponse, error) {
	var out DomainApplyResponse
	err := c.doWithIdempotencyKey(ctx, "POST", "/v1/domains/apply", request, &out, request.ExpectedPlanSHA256)
	return out, err
}
