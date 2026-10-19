// Package stripeinvoice creates draft invoices in a customer's own Stripe
// account from finalized usage statements (ADR-1502). It is separate from
// pkg/billing/stripe, which bills Gregale's own customers with Gregale's
// key: every call here takes the customer's key explicitly and never logs it.
package stripeinvoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIVersion pins request and response shapes, including invoice currency
// and pending_invoice_items_behavior.
const APIVersion = "2024-06-20"

// LineMetadataKey marks the invoice items an export created, so a resumed
// export skips lines Stripe already holds.
const LineMetadataKey = "gregale_line"

var (
	// ErrKeyRejected reports a key Stripe does not accept or that lacks the
	// invoice permissions an export needs.
	ErrKeyRejected = errors.New("stripe rejected the key")
	// ErrKeyFormat reports a value that is not a Stripe secret or
	// restricted key.
	ErrKeyFormat = errors.New("stripe key must start with sk_live_, sk_test_, rk_live_, or rk_test_")
)

// Error is a Stripe API error. Message is Stripe's own text, which never
// contains the key.
type Error struct {
	Status  int
	Type    string
	Code    string
	Param   string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("stripe %d %s: %s", e.Status, e.Code, e.Message)
}

// Client calls the Stripe REST API. BaseURL is overridable for tests.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for api.stripe.com.
func New() *Client {
	return &Client{BaseURL: "https://api.stripe.com", HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// KeyLivemode reports whether a Stripe secret or restricted key is live.
func KeyLivemode(key string) (bool, error) {
	switch {
	case strings.HasPrefix(key, "sk_live_"), strings.HasPrefix(key, "rk_live_"):
		return true, nil
	case strings.HasPrefix(key, "sk_test_"), strings.HasPrefix(key, "rk_test_"):
		return false, nil
	}
	return false, ErrKeyFormat
}

// DashboardURL links to an invoice in the Stripe dashboard.
func DashboardURL(livemode bool, invoiceID string) string {
	if livemode {
		return "https://dashboard.stripe.com/invoices/" + invoiceID
	}
	return "https://dashboard.stripe.com/test/invoices/" + invoiceID
}

func (c *Client) do(ctx context.Context, key, method, path, idempotencyKey string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return fmt.Errorf("stripe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Stripe-Version", APIVersion)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("stripe %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("stripe %s %s: read: %w", method, path, err)
	}
	if res.StatusCode >= 300 {
		var envelope struct {
			Error Error `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		envelope.Error.Status = res.StatusCode
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: %w", ErrKeyRejected, &envelope.Error)
		}
		return &envelope.Error
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("stripe %s %s: decode: %w", method, path, err)
	}
	return nil
}

// CheckKey confirms the key can read invoices, which a key able to create
// them can always do.
func (c *Client) CheckKey(ctx context.Context, key string) error {
	return c.do(ctx, key, http.MethodGet, "/v1/invoices?limit=1", "", nil, nil)
}

// InvoiceParams describe a draft invoice. Pending items already on the
// customer are excluded, and Stripe does not finalize the draft on its own.
type InvoiceParams struct {
	Customer    string
	Currency    string
	Description string
	Metadata    map[string]string
	// CollectionMethod is how Stripe collects the invoice once it is
	// finalized (ADR-1311): charge_automatically or send_invoice, due
	// DaysUntilDue days later. Empty leaves Stripe's default.
	CollectionMethod string
	DaysUntilDue     int
	// AutomaticTax has Stripe Tax calculate the invoice's tax (ADR-1313);
	// it needs Stripe Tax set up and a customer address.
	AutomaticTax bool
}

// Invoice is the part of a Stripe invoice an export records.
type Invoice struct {
	ID       string `json:"id"`
	Livemode bool   `json:"livemode"`
	Total    int64  `json:"total"`
}

// CreateInvoice creates a draft invoice. idempotencyKey makes a retry
// within Stripe's 24-hour window return the same invoice.
func (c *Client) CreateInvoice(ctx context.Context, key, idempotencyKey string, p InvoiceParams) (Invoice, error) {
	form := url.Values{}
	form.Set("customer", p.Customer)
	form.Set("currency", strings.ToLower(p.Currency))
	form.Set("auto_advance", "false")
	form.Set("pending_invoice_items_behavior", "exclude")
	form.Set("description", p.Description)
	if p.CollectionMethod != "" {
		form.Set("collection_method", p.CollectionMethod)
	}
	if p.CollectionMethod == "send_invoice" {
		form.Set("days_until_due", strconv.Itoa(p.DaysUntilDue))
	}
	if p.AutomaticTax {
		form.Set("automatic_tax[enabled]", "true")
	}
	for k, v := range p.Metadata {
		form.Set("metadata["+k+"]", v)
	}
	var out Invoice
	return out, c.do(ctx, key, http.MethodPost, "/v1/invoices", idempotencyKey, form, &out)
}

// ItemParams describe one invoice item in the minor currency unit.
type ItemParams struct {
	Customer    string
	InvoiceID   string
	Currency    string
	AmountMinor int64
	Description string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Metadata    map[string]string
}

// CreateInvoiceItem adds one item to a draft invoice.
func (c *Client) CreateInvoiceItem(ctx context.Context, key, idempotencyKey string, p ItemParams) error {
	form := url.Values{}
	form.Set("customer", p.Customer)
	form.Set("invoice", p.InvoiceID)
	form.Set("currency", strings.ToLower(p.Currency))
	form.Set("amount", strconv.FormatInt(p.AmountMinor, 10))
	form.Set("description", p.Description)
	form.Set("period[start]", strconv.FormatInt(p.PeriodStart.Unix(), 10))
	form.Set("period[end]", strconv.FormatInt(p.PeriodEnd.Unix(), 10))
	for k, v := range p.Metadata {
		form.Set("metadata["+k+"]", v)
	}
	return c.do(ctx, key, http.MethodPost, "/v1/invoiceitems", idempotencyKey, form, nil)
}

// ExportedLines returns the LineMetadataKey values of the items already on
// an invoice, following Stripe's pagination.
func (c *Client) ExportedLines(ctx context.Context, key, invoiceID string) (map[string]bool, error) {
	seen := map[string]bool{}
	after := ""
	for page := 0; page < 10; page++ {
		q := url.Values{}
		q.Set("invoice", invoiceID)
		q.Set("limit", "100")
		if after != "" {
			q.Set("starting_after", after)
		}
		var out struct {
			Data []struct {
				ID       string            `json:"id"`
				Metadata map[string]string `json:"metadata"`
			} `json:"data"`
			HasMore bool `json:"has_more"`
		}
		if err := c.do(ctx, key, http.MethodGet, "/v1/invoiceitems?"+q.Encode(), "", nil, &out); err != nil {
			return nil, err
		}
		for _, item := range out.Data {
			if line, ok := item.Metadata[LineMetadataKey]; ok {
				seen[line] = true
			}
		}
		if !out.HasMore || len(out.Data) == 0 {
			return seen, nil
		}
		after = out.Data[len(out.Data)-1].ID
	}
	return nil, fmt.Errorf("stripe invoice %s has too many items", invoiceID)
}

// FinalizeInvoice finalizes a draft with auto_advance on, so Stripe
// collects it as its collection method says: it charges the customer's
// saved payment method, or emails the invoice (ADR-1311).
func (c *Client) FinalizeInvoice(ctx context.Context, key, idempotencyKey, invoiceID string) error {
	form := url.Values{}
	form.Set("auto_advance", "true")
	return c.do(ctx, key, http.MethodPost, "/v1/invoices/"+url.PathEscape(invoiceID)+"/finalize", idempotencyKey, form, nil)
}

// InvoiceState is the payment state of an invoice (ADR-1504). Deleted is
// true when Stripe no longer has the invoice (a deleted draft).
type InvoiceState struct {
	Status          string
	AmountPaid      int64
	AmountRemaining int64
	DueDate         *time.Time
	PaidAt          *time.Time
	HostedURL       string
	Deleted         bool
}

// GetInvoice reads an invoice's payment state.
func (c *Client) GetInvoice(ctx context.Context, key, invoiceID string) (InvoiceState, error) {
	var out struct {
		Status            string `json:"status"`
		AmountPaid        int64  `json:"amount_paid"`
		AmountRemaining   int64  `json:"amount_remaining"`
		DueDate           *int64 `json:"due_date"`
		HostedInvoiceURL  string `json:"hosted_invoice_url"`
		StatusTransitions struct {
			PaidAt *int64 `json:"paid_at"`
		} `json:"status_transitions"`
	}
	err := c.do(ctx, key, http.MethodGet, "/v1/invoices/"+url.PathEscape(invoiceID), "", nil, &out)
	var stripeErr *Error
	if errors.As(err, &stripeErr) && stripeErr.Status == http.StatusNotFound {
		return InvoiceState{Deleted: true}, nil
	}
	if err != nil {
		return InvoiceState{}, err
	}
	state := InvoiceState{Status: out.Status, AmountPaid: out.AmountPaid, AmountRemaining: out.AmountRemaining, HostedURL: out.HostedInvoiceURL}
	if out.DueDate != nil {
		due := time.Unix(*out.DueDate, 0).UTC()
		state.DueDate = &due
	}
	if out.StatusTransitions.PaidAt != nil {
		paid := time.Unix(*out.StatusTransitions.PaidAt, 0).UTC()
		state.PaidAt = &paid
	}
	return state, nil
}
