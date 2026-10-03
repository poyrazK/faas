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

// ListPlatformTenantsPage reads one stable cursor-paginated page of account tenants.
// Pass the returned NextPageToken to continue; an empty token means the list ended.
func (c *Client) ListPlatformTenantsPage(ctx context.Context, limit int, pageToken string) (PlatformTenantListResponse, error) {
	var out PlatformTenantListResponse
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}
	path := "/v1/account/platform-tenants"
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

func (c *Client) PlanPlatformTenantReconciliation(ctx context.Context, tenantID string, req PlanPlatformTenantReconciliationRequest) (PlatformTenantReconciliationPlanResponse, error) {
	var out PlatformTenantReconciliationPlanResponse
	err := c.do(ctx, "POST", "/v1/account/platform-tenants/"+url.PathEscape(tenantID)+"/reconciliation-plan", req, &out)
	return out, err
}

func (c *Client) ApplyPlatformTenantReconciliation(ctx context.Context, tenantID string, req ApplyPlatformTenantReconciliationRequest) (PlatformTenantReconciliationApplyResponse, error) {
	var out PlatformTenantReconciliationApplyResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/reconciliation-plan/apply"
	err := c.do(ctx, "POST", path, req, &out)
	return out, err
}

func (c *Client) ListPlatformTenantReconciliationReceipts(ctx context.Context, tenantID string, opts ListPlatformTenantReconciliationReceiptsOptions) (PlatformTenantReconciliationReceiptListResponse, error) {
	var out PlatformTenantReconciliationReceiptListResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/reconciliations"
	q := url.Values{}
	if opts.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(opts.PageSize))
	}
	if opts.PageToken != "" {
		q.Set("page_token", opts.PageToken)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) GetPlatformTenantReconciliationReceipt(ctx context.Context, tenantID, receiptID string) (PlatformTenantReconciliationReceiptResponse, error) {
	var out PlatformTenantReconciliationReceiptResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/reconciliations/" + url.PathEscape(receiptID)
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) PlanPlatformTenantOffboarding(ctx context.Context, tenantID string) (PlatformTenantOffboardingPlanResponse, error) {
	var out PlatformTenantOffboardingPlanResponse
	err := c.do(ctx, "POST", "/v1/account/platform-tenants/"+url.PathEscape(tenantID)+"/offboarding-plan", struct{}{}, &out)
	return out, err
}

func (c *Client) ApplyPlatformTenantOffboarding(ctx context.Context, tenantID string, req ApplyPlatformTenantOffboardingRequest) (PlatformTenantOffboardingApplyResponse, error) {
	var out PlatformTenantOffboardingApplyResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/offboarding-plan/apply"
	return out, c.do(ctx, "POST", path, req, &out)
}

func (c *Client) ListPlatformTenantOffboardingReceipts(ctx context.Context, tenantID string, opts ListPlatformTenantOffboardingReceiptsOptions) (PlatformTenantOffboardingReceiptListResponse, error) {
	var out PlatformTenantOffboardingReceiptListResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/offboardings"
	q := url.Values{}
	if opts.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(opts.PageSize))
	}
	if opts.PageToken != "" {
		q.Set("page_token", opts.PageToken)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) GetPlatformTenantOffboardingReceipt(ctx context.Context, tenantID, receiptID string) (PlatformTenantOffboardingReceiptResponse, error) {
	var out PlatformTenantOffboardingReceiptResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/offboardings/" + url.PathEscape(receiptID)
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) GetPlatformTenant(ctx context.Context, id string) (PlatformTenantDetailResponse, error) {
	var out PlatformTenantDetailResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+url.PathEscape(id), nil, &out)
}

func (c *Client) GetPlatformTenantActivation(ctx context.Context, id string) (PlatformTenantActivationResponse, error) {
	var out PlatformTenantActivationResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+url.PathEscape(id)+"/activation", nil, &out)
}

func (c *Client) GetPlatformTenantRequestBudget(ctx context.Context, id string) (PlatformTenantRequestBudgetResponse, error) {
	var out PlatformTenantRequestBudgetResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+url.PathEscape(id)+"/request-budget", nil, &out)
}

func (c *Client) GetPlatformTenantHostnamePolicy(ctx context.Context, id string) (PlatformTenantHostnamePolicyResponse, error) {
	var out PlatformTenantHostnamePolicyResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+url.PathEscape(id)+"/hostname-policy", nil, &out)
}

func (c *Client) SetPlatformTenantHostnamePolicy(ctx context.Context, id string, req SetPlatformTenantHostnamePolicyRequest) (PlatformTenantHostnamePolicyResponse, error) {
	var out PlatformTenantHostnamePolicyResponse
	return out, c.do(ctx, "PUT", "/v1/account/platform-tenants/"+url.PathEscape(id)+"/hostname-policy", req, &out)
}

func (c *Client) SetPlatformTenantRequestBudget(ctx context.Context, id string, req SetPlatformTenantRequestBudgetRequest) (PlatformTenantRequestBudgetResponse, error) {
	var out PlatformTenantRequestBudgetResponse
	return out, c.do(ctx, "PUT", "/v1/account/platform-tenants/"+url.PathEscape(id)+"/request-budget", req, &out)
}

func (c *Client) ListPlatformTenantRateCards(ctx context.Context, tenantID string) (PlatformTenantRateCardListResponse, error) {
	var out PlatformTenantRateCardListResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/rate-cards"
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) CreatePlatformTenantRateCard(ctx context.Context, tenantID string, req CreatePlatformTenantRateCardRequest) (PlatformTenantRateCardResponse, error) {
	var out PlatformTenantRateCardResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/rate-cards"
	return out, c.do(ctx, "POST", path, req, &out)
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

// GetPlatformTenantSelfUsage reads cross-app usage using a tenant-bound
// platform tenant access token. The tenant is derived from the credential.
func (c *Client) GetPlatformTenantSelfUsage(ctx context.Context, opts APIConsumerUsageOptions) (PlatformTenantUsageResponse, error) {
	var out PlatformTenantUsageResponse
	path := "/v1/platform-tenant-self/usage"
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

// GetPlatformTenantSelfActivation reads the caller tenant's redacted,
// cross-app activation snapshot using a tenant-bound activation:read token.
func (c *Client) GetPlatformTenantSelfActivation(ctx context.Context) (PlatformTenantSelfActivationResponse, error) {
	var out PlatformTenantSelfActivationResponse
	return out, c.do(ctx, "GET", "/v1/platform-tenant-self/activation", nil, &out)
}

func (c *Client) CreatePlatformTenantSelfHostname(ctx context.Context, req CreatePlatformTenantSelfHostnameRequest) (PlatformTenantSelfHostnameResponse, error) {
	var out PlatformTenantSelfHostnameResponse
	return out, c.do(ctx, "POST", "/v1/platform-tenant-self/hostnames", req, &out)
}

// ListPlatformTenantSelfStatements returns only finalized statements owned by
// the tenant represented by the caller's access token.
func (c *Client) ListPlatformTenantSelfStatements(ctx context.Context, start, end time.Time, limit, offset int) (PlatformTenantSelfStatementListResponse, error) {
	var out PlatformTenantSelfStatementListResponse
	q := url.Values{}
	q.Set("period_start", start.UTC().Format(time.RFC3339))
	q.Set("period_end", end.UTC().Format(time.RFC3339))
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return out, c.do(ctx, "GET", "/v1/platform-tenant-self/usage-statements?"+q.Encode(), nil, &out)
}

func (c *Client) GetPlatformTenantSelfStatement(ctx context.Context, statementID string) (PlatformTenantStatementResponse, error) {
	var out PlatformTenantStatementResponse
	path := "/v1/platform-tenant-self/usage-statements/" + url.PathEscape(statementID)
	return out, c.do(ctx, "GET", path, nil, &out)
}

func platformTenantAccessTokensPath(tenantID string) string {
	return "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/access-tokens"
}

func (c *Client) ListPlatformTenantAccessTokens(ctx context.Context, tenantID string) (PlatformTenantAccessTokenListResponse, error) {
	var out PlatformTenantAccessTokenListResponse
	return out, c.do(ctx, "GET", platformTenantAccessTokensPath(tenantID), nil, &out)
}

func (c *Client) CreatePlatformTenantAccessToken(ctx context.Context, tenantID string, req CreatePlatformTenantAccessTokenRequest) (CreatePlatformTenantAccessTokenResponse, error) {
	var out CreatePlatformTenantAccessTokenResponse
	return out, c.do(ctx, "POST", platformTenantAccessTokensPath(tenantID), req, &out)
}

func (c *Client) RevokePlatformTenantAccessToken(ctx context.Context, tenantID, tokenID string) (PlatformTenantAccessTokenResponse, error) {
	var out PlatformTenantAccessTokenResponse
	path := platformTenantAccessTokensPath(tenantID) + "/" + url.PathEscape(tokenID)
	return out, c.do(ctx, "DELETE", path, nil, &out)
}

func (c *Client) ListPlatformTenantActivity(ctx context.Context, id string, opts PlatformTenantActivityOptions) (PlatformTenantActivityResponse, error) {
	var out PlatformTenantActivityResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(id) + "/activity"
	q := url.Values{}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.AppID != "" {
		q.Set("app_id", opts.AppID)
	}
	if opts.Status != 0 {
		q.Set("status", strconv.Itoa(opts.Status))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
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

func platformTenantWebhooksPath(tenantID string) string {
	return "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/webhooks"
}

func (c *Client) ListPlatformTenantWebhooks(ctx context.Context, tenantID string) (PlatformTenantWebhookListResponse, error) {
	var out PlatformTenantWebhookListResponse
	return out, c.do(ctx, "GET", platformTenantWebhooksPath(tenantID), nil, &out)
}

func (c *Client) CreatePlatformTenantWebhook(ctx context.Context, tenantID string, req CreatePlatformTenantWebhookRequest) (PlatformTenantWebhookResponse, error) {
	var out PlatformTenantWebhookResponse
	return out, c.do(ctx, "POST", platformTenantWebhooksPath(tenantID), req, &out)
}

func (c *Client) GetPlatformTenantWebhook(ctx context.Context, tenantID, webhookID string) (PlatformTenantWebhookResponse, error) {
	var out PlatformTenantWebhookResponse
	path := platformTenantWebhooksPath(tenantID) + "/" + url.PathEscape(webhookID)
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) UpdatePlatformTenantWebhook(ctx context.Context, tenantID, webhookID string, req UpdatePlatformTenantWebhookRequest) (PlatformTenantWebhookResponse, error) {
	var out PlatformTenantWebhookResponse
	path := platformTenantWebhooksPath(tenantID) + "/" + url.PathEscape(webhookID)
	return out, c.do(ctx, "PATCH", path, req, &out)
}

func (c *Client) DeletePlatformTenantWebhook(ctx context.Context, tenantID, webhookID string) error {
	path := platformTenantWebhooksPath(tenantID) + "/" + url.PathEscape(webhookID)
	return c.do(ctx, "DELETE", path, nil, nil)
}

func (c *Client) RotatePlatformTenantWebhookSecret(ctx context.Context, tenantID, webhookID string, req RotateAppWebhookSecretRequest) (RotateAppWebhookSecretResponse, error) {
	var out RotateAppWebhookSecretResponse
	path := platformTenantWebhooksPath(tenantID) + "/" + url.PathEscape(webhookID) + "/rotate-secret"
	return out, c.do(ctx, "POST", path, req, &out)
}

func (c *Client) ListPlatformTenantWebhookDeliveries(ctx context.Context, tenantID, webhookID string, opts ListAppWebhookDeliveriesOptions) (AppWebhookDeliveryListResponse, error) {
	var out AppWebhookDeliveryListResponse
	path := platformTenantWebhooksPath(tenantID) + "/" + url.PathEscape(webhookID) + "/deliveries"
	q := url.Values{}
	if opts.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(opts.PageSize))
	}
	if opts.PageToken != "" {
		q.Set("page_token", opts.PageToken)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) ListPlatformTenantWebhookDeliveryAttempts(ctx context.Context, tenantID, webhookID, deliveryID string, opts ListAppWebhookDeliveryAttemptsOptions) (AppWebhookDeliveryAttemptListResponse, error) {
	path := platformTenantWebhooksPath(tenantID) + "/" + url.PathEscape(webhookID) + "/deliveries/" + url.PathEscape(deliveryID) + "/attempts"
	return c.listWebhookAttemptPath(ctx, path, opts)
}

func (c *Client) GetPlatformTenantWebhookDeliveryHealth(ctx context.Context, tenantID, webhookID string) (AppWebhookDeliveryHealthResponse, error) {
	var out AppWebhookDeliveryHealthResponse
	path := platformTenantWebhooksPath(tenantID) + "/" + url.PathEscape(webhookID) + "/health"
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) RetryPlatformTenantWebhookDelivery(ctx context.Context, tenantID, webhookID, deliveryID string) (AppWebhookRetryDeliveryResponse, error) {
	var out AppWebhookRetryDeliveryResponse
	path := platformTenantWebhooksPath(tenantID) + "/" + url.PathEscape(webhookID) + "/deliveries/" + url.PathEscape(deliveryID) + "/retry"
	return out, c.do(ctx, "POST", path, nil, &out)
}

func (c *Client) GetPlatformTenantSelfInvocation(ctx context.Context, id string) (PlatformTenantInvocationResponse, error) {
	var out PlatformTenantInvocationResponse
	return out, c.do(ctx, "GET", "/v1/platform-tenant-self/invocations/"+url.PathEscape(id), nil, &out)
}
func (c *Client) CancelPlatformTenantSelfInvocation(ctx context.Context, id string) (PlatformTenantInvocationResponse, error) {
	var out PlatformTenantInvocationResponse
	return out, c.do(ctx, "POST", "/v1/platform-tenant-self/invocations/"+url.PathEscape(id)+"/cancel", nil, &out)
}
func (c *Client) ReplayPlatformTenantSelfInvocation(ctx context.Context, id string) (AsyncInvokeResponse, error) {
	var out AsyncInvokeResponse
	return out, c.do(ctx, "POST", "/v1/platform-tenant-self/invocations/"+url.PathEscape(id)+"/replay", nil, &out)
}
