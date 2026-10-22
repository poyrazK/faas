package api

import (
	"context"
	"time"
)

// CodeConsumerSpendCapReached is the gateway's 402 for a consumer whose
// month-to-date amount due passed its spend cap (ADR-1306).
const CodeConsumerSpendCapReached = "consumer_spend_cap_reached"

// CodeTenantSpendCapReached is the gateway's 402 for a platform tenant whose
// month to date passed its spend cap (ADR-20261019100331).
const CodeTenantSpendCapReached = "tenant_spend_cap_reached"

// Credit grant statuses.
const (
	ConsumerCreditActive  = "active"
	ConsumerCreditUsed    = "used"
	ConsumerCreditExpired = "expired"
	ConsumerCreditVoided  = "voided"
)

// CreateAPIConsumerCreditRequest grants a consumer prepaid credit
// (ADR-1306). Finalized statements in Currency draw it down until it is
// used, expires or is voided.
type CreateAPIConsumerCreditRequest struct {
	Currency         string     `json:"currency"`
	AmountMillicents int64      `json:"amount_millicents"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	Note             string     `json:"note,omitempty"`
}

// APIConsumerCreditResponse is one prepaid credit grant.
type APIConsumerCreditResponse struct {
	ID                  string     `json:"id"`
	ConsumerID          string     `json:"consumer_id"`
	Currency            string     `json:"currency"`
	AmountMillicents    int64      `json:"amount_millicents"`
	RemainingMillicents int64      `json:"remaining_millicents"`
	Status              string     `json:"status"`
	Note                string     `json:"note,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	VoidedAt            *time.Time `json:"voided_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

// APIConsumerCreditBalance is the credit a consumer can still use in one
// currency.
type APIConsumerCreditBalance struct {
	Currency            string `json:"currency"`
	AvailableMillicents int64  `json:"available_millicents"`
}

// APIConsumerCreditListResponse lists a consumer's grants, newest first,
// with the usable balance per currency.
type APIConsumerCreditListResponse struct {
	Credits  []APIConsumerCreditResponse `json:"credits"`
	Balances []APIConsumerCreditBalance  `json:"balances"`
}

// PutAPIConsumerSpendCapRequest sets a consumer's monthly spend cap: once
// the month's amount due, after prepaid credit, passes it, the gateway
// refuses the consumer's requests until the month ends or the cap or credit
// changes. A zero cap allows prepaid usage only.
type PutAPIConsumerSpendCapRequest struct {
	Currency             string `json:"currency"`
	MonthlyCapMillicents *int64 `json:"monthly_cap_millicents"`
}

// APIConsumerSpendCapResponse is a consumer's spend cap and its last check.
type APIConsumerSpendCapResponse struct {
	ConsumerID           string     `json:"consumer_id"`
	Currency             string     `json:"currency"`
	MonthlyCapMillicents int64      `json:"monthly_cap_millicents"`
	Capped               bool       `json:"capped"`
	Month                string     `json:"month,omitempty"`
	AmountDueMillicents  *int64     `json:"amount_due_millicents,omitempty"`
	CheckedAt            *time.Time `json:"checked_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func consumerPath(slug, consumerID string) string {
	return "/v1/apps/" + slug + "/consumers/" + consumerID
}

// CreateAPIConsumerCredit grants a consumer prepaid credit.
func (c *Client) CreateAPIConsumerCredit(ctx context.Context, slug, consumerID string, req CreateAPIConsumerCreditRequest) (APIConsumerCreditResponse, error) {
	var out APIConsumerCreditResponse
	return out, c.do(ctx, "POST", consumerPath(slug, consumerID)+"/credits", req, &out)
}

// ListAPIConsumerCredits lists a consumer's credit grants and balances.
func (c *Client) ListAPIConsumerCredits(ctx context.Context, slug, consumerID string) (APIConsumerCreditListResponse, error) {
	var out APIConsumerCreditListResponse
	return out, c.do(ctx, "GET", consumerPath(slug, consumerID)+"/credits", nil, &out)
}

// VoidAPIConsumerCredit voids what is left of a credit grant.
func (c *Client) VoidAPIConsumerCredit(ctx context.Context, slug, consumerID, creditID string) (APIConsumerCreditResponse, error) {
	var out APIConsumerCreditResponse
	return out, c.do(ctx, "DELETE", consumerPath(slug, consumerID)+"/credits/"+creditID, nil, &out)
}

// PutAPIConsumerSpendCap sets or replaces a consumer's monthly spend cap.
func (c *Client) PutAPIConsumerSpendCap(ctx context.Context, slug, consumerID string, req PutAPIConsumerSpendCapRequest) (APIConsumerSpendCapResponse, error) {
	var out APIConsumerSpendCapResponse
	return out, c.do(ctx, "PUT", consumerPath(slug, consumerID)+"/spend-cap", req, &out)
}

// GetAPIConsumerSpendCap reads a consumer's spend cap and its last check.
func (c *Client) GetAPIConsumerSpendCap(ctx context.Context, slug, consumerID string) (APIConsumerSpendCapResponse, error) {
	var out APIConsumerSpendCapResponse
	return out, c.do(ctx, "GET", consumerPath(slug, consumerID)+"/spend-cap", nil, &out)
}

// DeleteAPIConsumerSpendCap removes a consumer's spend cap.
func (c *Client) DeleteAPIConsumerSpendCap(ctx context.Context, slug, consumerID string) error {
	return c.do(ctx, "DELETE", consumerPath(slug, consumerID)+"/spend-cap", nil, nil)
}

// PlatformTenantCreditResponse is one prepaid credit grant to a platform
// tenant (ADR-20261019100331). Requests reuse
// CreateAPIConsumerCreditRequest; finalized tenant statements draw it down.
type PlatformTenantCreditResponse struct {
	ID                  string     `json:"id"`
	TenantID            string     `json:"tenant_id"`
	Currency            string     `json:"currency"`
	AmountMillicents    int64      `json:"amount_millicents"`
	RemainingMillicents int64      `json:"remaining_millicents"`
	Status              string     `json:"status"`
	Note                string     `json:"note,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	VoidedAt            *time.Time `json:"voided_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

// PlatformTenantCreditListResponse lists a tenant's grants, newest first,
// with the usable balance per currency.
type PlatformTenantCreditListResponse struct {
	Credits  []PlatformTenantCreditResponse `json:"credits"`
	Balances []APIConsumerCreditBalance     `json:"balances"`
}

// PlatformTenantSpendCapResponse is a tenant's monthly spend cap and its
// last check. Requests reuse PutAPIConsumerSpendCapRequest.
type PlatformTenantSpendCapResponse struct {
	TenantID             string     `json:"tenant_id"`
	Currency             string     `json:"currency"`
	MonthlyCapMillicents int64      `json:"monthly_cap_millicents"`
	Capped               bool       `json:"capped"`
	Month                string     `json:"month,omitempty"`
	AmountDueMillicents  *int64     `json:"amount_due_millicents,omitempty"`
	CheckedAt            *time.Time `json:"checked_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func tenantCreditsPath(tenantID string) string {
	return "/v1/account/platform-tenants/" + tenantID
}

// CreatePlatformTenantCredit grants a platform tenant prepaid credit.
func (c *Client) CreatePlatformTenantCredit(ctx context.Context, tenantID string, req CreateAPIConsumerCreditRequest) (PlatformTenantCreditResponse, error) {
	var out PlatformTenantCreditResponse
	return out, c.do(ctx, "POST", tenantCreditsPath(tenantID)+"/credits", req, &out)
}

// ListPlatformTenantCredits lists a tenant's credit grants and balances.
func (c *Client) ListPlatformTenantCredits(ctx context.Context, tenantID string) (PlatformTenantCreditListResponse, error) {
	var out PlatformTenantCreditListResponse
	return out, c.do(ctx, "GET", tenantCreditsPath(tenantID)+"/credits", nil, &out)
}

// VoidPlatformTenantCredit voids what is left of a tenant credit grant.
func (c *Client) VoidPlatformTenantCredit(ctx context.Context, tenantID, creditID string) (PlatformTenantCreditResponse, error) {
	var out PlatformTenantCreditResponse
	return out, c.do(ctx, "DELETE", tenantCreditsPath(tenantID)+"/credits/"+creditID, nil, &out)
}

// PutPlatformTenantSpendCap sets or replaces a tenant's monthly spend cap.
func (c *Client) PutPlatformTenantSpendCap(ctx context.Context, tenantID string, req PutAPIConsumerSpendCapRequest) (PlatformTenantSpendCapResponse, error) {
	var out PlatformTenantSpendCapResponse
	return out, c.do(ctx, "PUT", tenantCreditsPath(tenantID)+"/spend-cap", req, &out)
}

// GetPlatformTenantSpendCap reads a tenant's spend cap and its last check.
func (c *Client) GetPlatformTenantSpendCap(ctx context.Context, tenantID string) (PlatformTenantSpendCapResponse, error) {
	var out PlatformTenantSpendCapResponse
	return out, c.do(ctx, "GET", tenantCreditsPath(tenantID)+"/spend-cap", nil, &out)
}

// DeletePlatformTenantSpendCap removes a tenant's spend cap.
func (c *Client) DeletePlatformTenantSpendCap(ctx context.Context, tenantID string) error {
	return c.do(ctx, "DELETE", tenantCreditsPath(tenantID)+"/spend-cap", nil, nil)
}
