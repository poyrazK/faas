package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// CreateBillingRunRequest bills one ended UTC month for every customer with
// billable usage (ADR-1503). Omitted fields default to both sources,
// finalize, no export and holding consumers with completeness gaps.
type CreateBillingRunRequest struct {
	Month      string   `json:"month"`
	Sources    []string `json:"sources,omitempty"`
	Finalize   *bool    `json:"finalize,omitempty"`
	Export     string   `json:"export,omitempty"`
	HoldOnGaps *bool    `json:"hold_on_gaps,omitempty"`
}

// BillingRunCounts counts a run's items by outcome.
type BillingRunCounts struct {
	Pending   int `json:"pending"`
	Drafted   int `json:"drafted"`
	Finalized int `json:"finalized"`
	Exported  int `json:"exported"`
	UpToDate  int `json:"up_to_date"`
	Held      int `json:"held"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

// BillingRunResponse is a billing run and its progress.
type BillingRunResponse struct {
	ID          string           `json:"id"`
	Month       string           `json:"month"`
	PeriodStart time.Time        `json:"period_start"`
	PeriodEnd   time.Time        `json:"period_end"`
	Sources     []string         `json:"sources"`
	Finalize    bool             `json:"finalize"`
	Export      string           `json:"export"`
	HoldOnGaps  bool             `json:"hold_on_gaps"`
	Trigger     string           `json:"trigger"`
	Status      string           `json:"status"`
	Counts      BillingRunCounts `json:"counts"`
	CreatedAt   time.Time        `json:"created_at"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
}

// BillingRunListResponse lists an account's runs, newest first.
type BillingRunListResponse struct {
	Runs []BillingRunResponse `json:"runs"`
}

// BillingRunItemResponse is what a run did for one customer.
type BillingRunItemResponse struct {
	SourceKind       string    `json:"source_kind"`
	AppID            string    `json:"app_id,omitempty"`
	ConsumerID       string    `json:"consumer_id,omitempty"`
	PlatformTenantID string    `json:"platform_tenant_id,omitempty"`
	ExternalRef      string    `json:"external_ref"`
	Outcome          string    `json:"outcome"`
	StatementID      string    `json:"statement_id,omitempty"`
	Revision         int       `json:"revision,omitempty"`
	Currency         string    `json:"currency,omitempty"`
	AmountMillicents *int64    `json:"amount_millicents,omitempty"`
	Completeness     string    `json:"completeness,omitempty"`
	InvoiceID        string    `json:"invoice_id,omitempty"`
	ExportedInvoices int       `json:"exported_invoices"`
	Reason           string    `json:"reason,omitempty"`
	Attempts         int       `json:"attempts"`
	UpdatedAt        time.Time `json:"updated_at"`
	// PaymentStatus is the Stripe invoice status of the item's statement
	// (ADR-1504).
	PaymentStatus string `json:"payment_status,omitempty"`
}

// BillingRunItemListResponse pages a run's items; NextCursor continues it.
type BillingRunItemListResponse struct {
	Items      []BillingRunItemResponse `json:"items"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

// PutBillingRunScheduleRequest starts a run for each month after it ends.
// Omitted fields default like CreateBillingRunRequest, with a 24-hour delay.
type PutBillingRunScheduleRequest struct {
	Sources    []string `json:"sources,omitempty"`
	Finalize   *bool    `json:"finalize,omitempty"`
	Export     string   `json:"export,omitempty"`
	HoldOnGaps *bool    `json:"hold_on_gaps,omitempty"`
	DelayHours *int     `json:"delay_hours,omitempty"`
}

// BillingRunScheduleResponse is the account's billing run schedule.
type BillingRunScheduleResponse struct {
	Sources    []string  `json:"sources"`
	Finalize   bool      `json:"finalize"`
	Export     string    `json:"export"`
	HoldOnGaps bool      `json:"hold_on_gaps"`
	DelayHours int       `json:"delay_hours"`
	NextMonth  string    `json:"next_month"`
	NextRunAt  time.Time `json:"next_run_at"`
	LastMonth  string    `json:"last_month,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreateBillingRun starts a billing run for an ended month.
func (c *Client) CreateBillingRun(ctx context.Context, req CreateBillingRunRequest) (BillingRunResponse, error) {
	var out BillingRunResponse
	return out, c.do(ctx, "POST", "/v1/account/billing-runs", req, &out)
}

// ListBillingRuns lists the account's billing runs, newest first.
func (c *Client) ListBillingRuns(ctx context.Context, limit int) (BillingRunListResponse, error) {
	var out BillingRunListResponse
	path := "/v1/account/billing-runs"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetBillingRun returns one billing run.
func (c *Client) GetBillingRun(ctx context.Context, runID string) (BillingRunResponse, error) {
	var out BillingRunResponse
	return out, c.do(ctx, "GET", "/v1/account/billing-runs/"+url.PathEscape(runID), nil, &out)
}

// ListBillingRunItems pages a run's items, optionally with one outcome.
func (c *Client) ListBillingRunItems(ctx context.Context, runID, outcome, cursor string, limit int) (BillingRunItemListResponse, error) {
	var out BillingRunItemListResponse
	q := url.Values{}
	if outcome != "" {
		q.Set("outcome", outcome)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/account/billing-runs/" + url.PathEscape(runID) + "/items"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// PutBillingRunSchedule creates or replaces the account's schedule.
func (c *Client) PutBillingRunSchedule(ctx context.Context, req PutBillingRunScheduleRequest) (BillingRunScheduleResponse, error) {
	var out BillingRunScheduleResponse
	return out, c.do(ctx, "PUT", "/v1/account/billing-run-schedule", req, &out)
}

// GetBillingRunSchedule returns the account's schedule.
func (c *Client) GetBillingRunSchedule(ctx context.Context) (BillingRunScheduleResponse, error) {
	var out BillingRunScheduleResponse
	return out, c.do(ctx, "GET", "/v1/account/billing-run-schedule", nil, &out)
}

// DeleteBillingRunSchedule stops scheduled runs; started runs continue.
func (c *Client) DeleteBillingRunSchedule(ctx context.Context) error {
	return c.do(ctx, "DELETE", "/v1/account/billing-run-schedule", nil, nil)
}
