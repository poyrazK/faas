// Package lagoinvoice creates one-off invoices in a customer's own Lago
// (Cloud or self-hosted) from finalized usage statements (ADR-1507). Every
// call takes the customer's API key explicitly and never logs it.
package lagoinvoice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultURL is Lago Cloud's US API host.
const DefaultURL = "https://api.getlago.com"

// AddOnCode is the add-on every exported fee uses.
const AddOnCode = "gregale_usage"

// ExportMetadataKey names the export on the invoice so a retry finds it.
const ExportMetadataKey = "gregale_export_id"

var (
	// ErrKeyRejected reports a key Lago does not accept.
	ErrKeyRejected = errors.New("lago rejected the key")
	// ErrAPIURL reports an API URL that is not a bare https:// origin.
	ErrAPIURL = errors.New("lago api_url must be an https:// URL without a query or credentials")
)

// Error is a Lago API error; Code and Detail are Lago's own text.
type Error struct {
	Status int
	Code   string
	Detail string
}

func (e *Error) Error() string { return fmt.Sprintf("lago %d %s: %s", e.Status, e.Code, e.Detail) }

// Client calls a Lago API. HTTP must refuse private addresses in production,
// since self-hosted URLs are customer input.
type Client struct {
	HTTP *http.Client
}

// NormalizeURL validates an API URL and returns it without a trailing slash
// or /api/v1 suffix; empty means DefaultURL.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 512 {
		return "", ErrAPIURL
	}
	path := strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/api/v1")
	return "https://" + u.Host + strings.TrimRight(path, "/"), nil
}

func (c *Client) do(ctx context.Context, baseURL, key, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("lago encode: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(baseURL, "/")+"/api/v1"+path, reader)
	if err != nil {
		return fmt.Errorf("lago request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// codeql[go/request-forgery] false-positive: NormalizeURL admits only a bare https:// origin, and apid's client dials through oci.EgressDialContext, which refuses loopback, private and metadata addresses at connect time, redirects included.
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("lago %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("lago %s %s: read: %w", method, path, err)
	}
	if res.StatusCode >= 300 {
		var body struct {
			Error   string          `json:"error"`
			Code    string          `json:"code"`
			Details json.RawMessage `json:"error_details"`
		}
		_ = json.Unmarshal(raw, &body)
		e := &Error{Status: res.StatusCode, Code: body.Code, Detail: body.Error}
		if len(body.Details) > 0 && len(body.Details) < 512 {
			e.Detail += " " + string(body.Details)
		}
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: %w", ErrKeyRejected, e)
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("lago %s %s: decode: %w", method, path, err)
	}
	return nil
}

func isNotFound(err error) bool {
	var lagoErr *Error
	return errors.As(err, &lagoErr) && lagoErr.Status == http.StatusNotFound
}

// CheckKey confirms the key can read add-ons.
func (c *Client) CheckKey(ctx context.Context, baseURL, key string) error {
	return c.do(ctx, baseURL, key, http.MethodGet, "/add_ons?per_page=1", nil, nil)
}

// EnsureAddOn creates the gregale_usage add-on when it is missing. Fees
// override its amount, so its own price is zero.
func (c *Client) EnsureAddOn(ctx context.Context, baseURL, key, currency string) error {
	err := c.do(ctx, baseURL, key, http.MethodGet, "/add_ons/"+AddOnCode, nil, nil)
	if !isNotFound(err) {
		return err
	}
	err = c.do(ctx, baseURL, key, http.MethodPost, "/add_ons", map[string]any{"add_on": map[string]any{
		"name": "API usage", "code": AddOnCode, "amount_cents": 0, "amount_currency": strings.ToUpper(currency),
		"description": "Usage exported from Gregale usage statements"}}, nil)
	var lagoErr *Error
	if errors.As(err, &lagoErr) && lagoErr.Status == http.StatusUnprocessableEntity && strings.Contains(lagoErr.Detail, "value_already_exist") {
		return nil
	}
	return err
}

// Fee is one invoice fee in cents; Lago rejects negative amounts.
type Fee struct {
	Description string
	AmountCents int64
}

// InvoiceParams describe a one-off invoice.
type InvoiceParams struct {
	ExternalCustomerID string
	Currency           string
	Fees               []Fee
	From, To           time.Time
	// Charge lets Lago send the invoice to the operator's payment provider,
	// which charges the customer's saved payment method (ADR-1311).
	Charge bool
}

type metadataItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type invoiceData struct {
	LagoID               string         `json:"lago_id"`
	InvoiceType          string         `json:"invoice_type"`
	Status               string         `json:"status"`
	PaymentStatus        string         `json:"payment_status"`
	Currency             string         `json:"currency"`
	PaymentDueDate       string         `json:"payment_due_date"`
	FeesAmountCents      int64          `json:"fees_amount_cents"`
	TotalAmountCents     int64          `json:"total_amount_cents"`
	TotalPaidAmountCents int64          `json:"total_paid_amount_cents"`
	TotalDueAmountCents  int64          `json:"total_due_amount_cents"`
	WebURL               string         `json:"web_url"`
	UpdatedAt            *time.Time     `json:"updated_at"`
	Metadata             []metadataItem `json:"metadata"`
}

func (d invoiceData) metadata(key string) string {
	for _, m := range d.Metadata {
		if m.Key == key {
			return m.Value
		}
	}
	return ""
}

// CreateInvoice creates a one-off invoice, which Lago finalizes at once.
// Unless p.Charge is set, skip_psp keeps Lago from charging it through the
// operator's payment provider (ADR-1311).
func (c *Client) CreateInvoice(ctx context.Context, baseURL, key string, p InvoiceParams) (string, error) {
	fees := make([]map[string]any, 0, len(p.Fees))
	for _, f := range p.Fees {
		if f.AmountCents < 0 {
			return "", fmt.Errorf("lago fee %q has a negative amount", f.Description)
		}
		fees = append(fees, map[string]any{"add_on_code": AddOnCode, "units": "1", "unit_amount_cents": f.AmountCents,
			"description": f.Description, "from_datetime": p.From.UTC().Format(time.RFC3339), "to_datetime": p.To.UTC().Format(time.RFC3339)})
	}
	var out struct {
		Invoice invoiceData `json:"invoice"`
	}
	err := c.do(ctx, baseURL, key, http.MethodPost, "/invoices", map[string]any{"invoice": map[string]any{
		"external_customer_id": p.ExternalCustomerID, "currency": strings.ToUpper(p.Currency), "skip_psp": !p.Charge, "fees": fees}}, &out)
	if err != nil {
		return "", err
	}
	return out.Invoice.LagoID, nil
}

// SetExportMetadata tags the invoice with its export ID.
func (c *Client) SetExportMetadata(ctx context.Context, baseURL, key, lagoID, exportID, statementID string) error {
	return c.do(ctx, baseURL, key, http.MethodPut, "/invoices/"+url.PathEscape(lagoID), map[string]any{"invoice": map[string]any{
		"metadata": []metadataItem{{Key: ExportMetadataKey, Value: exportID}, {Key: "gregale_statement_id", Value: statementID}}}}, nil)
}

// FindInvoice returns the invoice tagged with exportID, or "".
func (c *Client) FindInvoice(ctx context.Context, baseURL, key, exportID string) (string, error) {
	q := url.Values{}
	q.Set("metadata["+ExportMetadataKey+"]", exportID)
	var out struct {
		Invoices []invoiceData `json:"invoices"`
	}
	if err := c.do(ctx, baseURL, key, http.MethodGet, "/invoices?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	for _, inv := range out.Invoices {
		if inv.metadata(ExportMetadataKey) == exportID && inv.Status != "deleted" {
			return inv.LagoID, nil
		}
	}
	return "", nil
}

// FindUntagged returns the customer's live one-off invoices issued since
// the given day in currency, with feesCents in fees and no export tag: what
// an export that crashed before tagging its invoice leaves behind.
func (c *Client) FindUntagged(ctx context.Context, baseURL, key, customer, currency string, since time.Time, feesCents int64) ([]string, error) {
	q := url.Values{}
	q.Set("external_customer_id", customer)
	q.Set("currency", strings.ToUpper(currency))
	q.Set("issuing_date_from", since.UTC().Format(time.DateOnly))
	q.Set("per_page", "100")
	var out struct {
		Invoices []invoiceData `json:"invoices"`
	}
	if err := c.do(ctx, baseURL, key, http.MethodGet, "/invoices?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	var ids []string
	for _, inv := range out.Invoices {
		oneOff := inv.InvoiceType == "one_off" || inv.InvoiceType == "add_on"
		live := inv.Status != "voided" && inv.Status != "deleted"
		if oneOff && live && inv.FeesAmountCents == feesCents && inv.metadata(ExportMetadataKey) == "" {
			ids = append(ids, inv.LagoID)
		}
	}
	return ids, nil
}

// InvoiceState is an invoice's payment state (ADR-1504).
type InvoiceState struct {
	Status        string
	PaymentStatus string
	TotalCents    int64
	PaidCents     int64
	DueCents      int64
	// DueAt is the end of payment_due_date in UTC.
	DueAt   *time.Time
	PaidAt  *time.Time
	WebURL  string
	Deleted bool
}

// GetInvoice reads an invoice's payment state.
func (c *Client) GetInvoice(ctx context.Context, baseURL, key, lagoID string) (InvoiceState, error) {
	var out struct {
		Invoice invoiceData `json:"invoice"`
	}
	err := c.do(ctx, baseURL, key, http.MethodGet, "/invoices/"+url.PathEscape(lagoID), nil, &out)
	if isNotFound(err) {
		return InvoiceState{Deleted: true}, nil
	}
	if err != nil {
		return InvoiceState{}, err
	}
	inv := out.Invoice
	st := InvoiceState{Status: inv.Status, PaymentStatus: inv.PaymentStatus, TotalCents: inv.TotalAmountCents,
		PaidCents: inv.TotalPaidAmountCents, DueCents: inv.TotalDueAmountCents, WebURL: inv.WebURL, Deleted: inv.Status == "deleted"}
	if day, err := time.Parse(time.DateOnly, inv.PaymentDueDate); err == nil {
		due := day.Add(24 * time.Hour)
		st.DueAt = &due
	}
	if inv.PaymentStatus == "succeeded" && inv.UpdatedAt != nil {
		at := inv.UpdatedAt.UTC()
		st.PaidAt = &at
	}
	return st, nil
}
