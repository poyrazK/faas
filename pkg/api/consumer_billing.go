package api

import (
	"context"
	"net/url"
	"time"
)

// ConsumerScopeBilling lets a consumer key read its own usage, spend and
// invoices (ADR-1506). It grants no access to the app itself.
const ConsumerScopeBilling = "billing"

// CodeConsumerBillingRateLimited reports that a consumer key exceeded the
// billing self-service rate limit (ADR-1506).
const CodeConsumerBillingRateLimited = "consumer_billing_rate_limited"

// ConsumerBillingPlan is the consumer's current plan and its monthly cap.
type ConsumerBillingPlan struct {
	Name             string `json:"name"`
	MaxUnitsPerMonth int64  `json:"max_units_per_month,omitempty"`
}

// ConsumerBillingSummaryResponse is a consumer's month to date.
type ConsumerBillingSummaryResponse struct {
	ConsumerID       string               `json:"consumer_id"`
	ExternalRef      string               `json:"external_ref"`
	Name             string               `json:"name"`
	App              string               `json:"app"`
	Month            string               `json:"month"`
	Currency         string               `json:"currency,omitempty"`
	BillableUnits    int64                `json:"billable_units"`
	UnpricedUnits    int64                `json:"unpriced_units"`
	AmountMillicents int64                `json:"amount_millicents"`
	Priced           bool                 `json:"priced"`
	Plan             *ConsumerBillingPlan `json:"plan,omitempty"`
	AsOf             time.Time            `json:"as_of"`
	// Credits is the prepaid credit the consumer can still use, per
	// currency; SpendCap is its monthly cap and last check (ADR-1306).
	Credits  []APIConsumerCreditBalance `json:"credits"`
	SpendCap *ConsumerBillingSpendCap   `json:"spend_cap,omitempty"`
}

// ConsumerBillingSpendCap is a consumer's monthly spend cap as the consumer
// sees it: once AmountDueMillicents passes the cap, requests are refused
// until the month ends or credit is added.
type ConsumerBillingSpendCap struct {
	Currency             string `json:"currency"`
	MonthlyCapMillicents int64  `json:"monthly_cap_millicents"`
	Capped               bool   `json:"capped"`
	AmountDueMillicents  *int64 `json:"amount_due_millicents,omitempty"`
}

// ConsumerBillingDay is one UTC day of a consumer's usage.
type ConsumerBillingDay struct {
	Date          string `json:"date"`
	Requests      int64  `json:"requests"`
	BillableUnits int64  `json:"billable_units"`
}

// ConsumerBillingUsageResponse lists a consumer's daily usage.
type ConsumerBillingUsageResponse struct {
	Since time.Time            `json:"since"`
	Until time.Time            `json:"until"`
	Days  []ConsumerBillingDay `json:"days"`
}

// ConsumerBillingInvoice is the customer-facing view of a statement's
// exported invoice.
type ConsumerBillingInvoice struct {
	PaymentStatus        string     `json:"payment_status,omitempty"`
	AmountMinor          int64      `json:"amount_minor"`
	AmountRemainingMinor *int64     `json:"amount_remaining_minor,omitempty"`
	DueAt                *time.Time `json:"due_at,omitempty"`
	PaidAt               *time.Time `json:"paid_at,omitempty"`
	HostedInvoiceURL     string     `json:"hosted_invoice_url,omitempty"`
}

// ConsumerBillingStatement is one finalized statement.
type ConsumerBillingStatement struct {
	ID               string                  `json:"id"`
	PeriodStart      time.Time               `json:"period_start"`
	PeriodEnd        time.Time               `json:"period_end"`
	Revision         int                     `json:"revision"`
	Currency         string                  `json:"currency"`
	BillableUnits    int64                   `json:"billable_units"`
	AmountMillicents int64                   `json:"amount_millicents"`
	FinalizedAt      *time.Time              `json:"finalized_at,omitempty"`
	Invoice          *ConsumerBillingInvoice `json:"invoice,omitempty"`
}

// ConsumerBillingStatementsResponse lists a consumer's latest finalized
// statements, newest first.
type ConsumerBillingStatementsResponse struct {
	Statements []ConsumerBillingStatement `json:"statements"`
}

// GetConsumerBillingSummary returns the consumer's month to date. The
// Client must be built with the consumer's key (NewClient(base, "ck_…")),
// not an operator credential (ADR-1506).
func (c *Client) GetConsumerBillingSummary(ctx context.Context, app string) (ConsumerBillingSummaryResponse, error) {
	var out ConsumerBillingSummaryResponse
	return out, c.do(ctx, "GET", "/v1/consumer-billing/"+url.PathEscape(app), nil, &out)
}

// GetConsumerBillingUsage returns the consumer's daily usage between since
// and until; zero times use the current month. Requires a consumer key.
func (c *Client) GetConsumerBillingUsage(ctx context.Context, app string, since, until time.Time) (ConsumerBillingUsageResponse, error) {
	var out ConsumerBillingUsageResponse
	q := url.Values{}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	if !until.IsZero() {
		q.Set("until", until.UTC().Format(time.RFC3339))
	}
	path := "/v1/consumer-billing/" + url.PathEscape(app) + "/usage"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ListConsumerBillingStatements returns the consumer's latest finalized
// statements. Requires a consumer key.
func (c *Client) ListConsumerBillingStatements(ctx context.Context, app string) (ConsumerBillingStatementsResponse, error) {
	var out ConsumerBillingStatementsResponse
	return out, c.do(ctx, "GET", "/v1/consumer-billing/"+url.PathEscape(app)+"/statements", nil, &out)
}

// Customer self-service with a billing-scope key (ADR-20261021183000).

// CodeConsumerSelfServiceUnavailable reports a self-service action the app
// owner does not offer, or one the customer's billing setup cannot do.
const CodeConsumerSelfServiceUnavailable = "consumer_self_service_unavailable"

// ConsumerBillingPlansResponse lists the plans a customer can switch
// between. CanChange is false unless the owner lets customers change plans.
type ConsumerBillingPlansResponse struct {
	CurrentPlanID string               `json:"current_plan_id,omitempty"`
	CanChange     bool                 `json:"can_change"`
	CanClose      bool                 `json:"can_close"`
	Plans         []ConsumerSignupPlan `json:"plans"`
}

// ChangeConsumerBillingPlanRequest switches the customer to one of the
// offered plans from the next minute.
type ChangeConsumerBillingPlanRequest struct {
	PlanID string `json:"plan_id"`
}

// ConsumerBillingPlanChangeResponse is the scheduled plan change.
type ConsumerBillingPlanChangeResponse struct {
	PlanID        string    `json:"plan_id"`
	EffectiveFrom time.Time `json:"effective_from"`
}

// CreateConsumerBillingPortalRequest opens the payment provider's customer
// portal; ReturnURL defaults to the app's sign-up return URL.
type CreateConsumerBillingPortalRequest struct {
	ReturnURL string `json:"return_url,omitempty"`
}

// ConsumerBillingPortalResponse is a short-lived portal URL to send the
// customer to.
type ConsumerBillingPortalResponse struct {
	URL string `json:"url"`
}

// ConsumerBillingCloseResponse is the closed account. The key stops
// working at once; usage until then is still billed.
type ConsumerBillingCloseResponse struct {
	ConsumerID string    `json:"consumer_id"`
	ClosedAt   time.Time `json:"closed_at"`
}

// ListConsumerBillingPlans lists the plans the consumer can switch
// between. Requires a consumer key.
func (c *Client) ListConsumerBillingPlans(ctx context.Context, app string) (ConsumerBillingPlansResponse, error) {
	var out ConsumerBillingPlansResponse
	return out, c.do(ctx, "GET", "/v1/consumer-billing/"+url.PathEscape(app)+"/plans", nil, &out)
}

// ChangeConsumerBillingPlan switches the consumer's plan. Requires a
// consumer key.
func (c *Client) ChangeConsumerBillingPlan(ctx context.Context, app string, req ChangeConsumerBillingPlanRequest) (ConsumerBillingPlanChangeResponse, error) {
	var out ConsumerBillingPlanChangeResponse
	return out, c.do(ctx, "PUT", "/v1/consumer-billing/"+url.PathEscape(app)+"/plan", req, &out)
}

// CreateConsumerBillingPortalSession returns a payment provider portal URL
// where the consumer updates their card and sees invoices. Requires a
// consumer key.
func (c *Client) CreateConsumerBillingPortalSession(ctx context.Context, app string, req CreateConsumerBillingPortalRequest) (ConsumerBillingPortalResponse, error) {
	var out ConsumerBillingPortalResponse
	return out, c.do(ctx, "POST", "/v1/consumer-billing/"+url.PathEscape(app)+"/portal", req, &out)
}

// CloseConsumerBillingAccount closes the consumer's account. Requires a
// consumer key, which stops working.
func (c *Client) CloseConsumerBillingAccount(ctx context.Context, app string) (ConsumerBillingCloseResponse, error) {
	var out ConsumerBillingCloseResponse
	return out, c.do(ctx, "POST", "/v1/consumer-billing/"+url.PathEscape(app)+"/close", struct{}{}, &out)
}
