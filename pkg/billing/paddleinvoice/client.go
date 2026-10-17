// Package paddleinvoice creates draft transactions in a customer's own
// Paddle Billing account from finalized usage statements (ADR-1507). Every
// call takes the customer's API key explicitly and never logs it.
package paddleinvoice

import (
	"bytes"
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

	"github.com/onebox-faas/faas/pkg/safetext"
)

// APIVersion is the Paddle-Version header value.
const APIVersion = "1"

// Live and sandbox API hosts.
const (
	LiveURL    = "https://api.paddle.com"
	SandboxURL = "https://sandbox-api.paddle.com"
)

// PaymentTermsDays is the payment window of a manually collected
// transaction Gregale creates.
const PaymentTermsDays = 30

var (
	// ErrKeyRejected reports a key Paddle does not accept or that lacks the
	// transaction permissions an export needs.
	ErrKeyRejected = errors.New("paddle rejected the key")
	// ErrKeyFormat reports a value that is not a Paddle Billing API key.
	ErrKeyFormat = errors.New("paddle key must start with pdl_live_apikey_ or pdl_sdbx_apikey_")
)

// Error is a Paddle API error; Detail is Paddle's own text.
type Error struct {
	Status int
	Type   string `json:"type"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (e *Error) Error() string { return fmt.Sprintf("paddle %d %s: %s", e.Status, e.Code, e.Detail) }

// Client calls the Paddle Billing API. BaseURL overrides the host chosen by
// the key's mode (tests).
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client that picks api.paddle.com or the sandbox by key.
func New() *Client { return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}} }

// KeyLivemode reports whether a Paddle API key is live.
func KeyLivemode(key string) (bool, error) {
	switch {
	case strings.HasPrefix(key, "pdl_live_apikey_"):
		return true, nil
	case strings.HasPrefix(key, "pdl_sdbx_apikey_"):
		return false, nil
	}
	return false, ErrKeyFormat
}

// DashboardURL links to a transaction in the Paddle dashboard.
func DashboardURL(livemode bool, transactionID string) string {
	if livemode {
		return "https://vendors.paddle.com/transactions-v2/" + transactionID
	}
	return "https://sandbox-vendors.paddle.com/transactions-v2/" + transactionID
}

func (c *Client) baseURL(key string) string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	if live, _ := KeyLivemode(key); live {
		return LiveURL
	}
	return SandboxURL
}

func (c *Client) do(ctx context.Context, key, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("paddle encode: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL(key)+path, reader)
	if err != nil {
		return fmt.Errorf("paddle request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Paddle-Version", APIVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("paddle %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("paddle %s %s: read: %w", method, path, err)
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
		return fmt.Errorf("paddle %s %s: decode: %w", method, path, err)
	}
	return nil
}

// CheckKey confirms the key can read transactions.
func (c *Client) CheckKey(ctx context.Context, key string) error {
	return c.do(ctx, key, http.MethodGet, "/transactions?per_page=1", nil, nil)
}

// Item is one transaction line in the currency's minor unit; Paddle rejects
// negative amounts.
type Item struct {
	Description string
	AmountMinor int64
}

// TransactionParams describe a draft manually collected transaction.
type TransactionParams struct {
	CustomerID string
	Currency   string
	Items      []Item
	CustomData map[string]string
}

type transactionData struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	CustomData   map[string]string `json:"custom_data"`
	BilledAt     *time.Time        `json:"billed_at"`
	CurrencyCode string            `json:"currency_code"`
	Details      struct {
		Totals struct {
			GrandTotal string `json:"grand_total"`
			Balance    string `json:"balance"`
		} `json:"totals"`
	} `json:"details"`
	BillingDetails *struct {
		PaymentTerms struct {
			Interval  string `json:"interval"`
			Frequency int    `json:"frequency"`
		} `json:"payment_terms"`
	} `json:"billing_details"`
	Checkout *struct {
		URL string `json:"url"`
	} `json:"checkout"`
	Payments []struct {
		Status     string     `json:"status"`
		CapturedAt *time.Time `json:"captured_at"`
	} `json:"payments"`
}

// FindTransaction returns the ID of the customer's recent transaction whose
// custom_data names exportID, or "" when there is none. Paddle has no
// idempotency keys, so a retried export adopts it instead of creating a
// second one.
func (c *Client) FindTransaction(ctx context.Context, key, customerID, exportID string) (string, error) {
	q := url.Values{}
	q.Set("customer_id", customerID)
	q.Set("order_by", "created_at[DESC]")
	q.Set("per_page", "100")
	var out struct {
		Data []transactionData `json:"data"`
	}
	if err := c.do(ctx, key, http.MethodGet, "/transactions?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	for _, t := range out.Data {
		if t.CustomData["gregale_export_id"] == exportID {
			return t.ID, nil
		}
	}
	return "", nil
}

// CreateTransaction creates a manually collected transaction without an
// address, so it stays a draft until the operator bills it in Paddle.
func (c *Client) CreateTransaction(ctx context.Context, key string, p TransactionParams) (string, error) {
	type price struct {
		Description string            `json:"description"`
		UnitPrice   map[string]string `json:"unit_price"`
		Product     map[string]string `json:"product"`
	}
	type item struct {
		Quantity int   `json:"quantity"`
		Price    price `json:"price"`
	}
	body := map[string]any{
		"customer_id":     p.CustomerID,
		"currency_code":   strings.ToUpper(p.Currency),
		"collection_mode": "manual",
		"billing_details": map[string]any{"payment_terms": map[string]any{"interval": "day", "frequency": PaymentTermsDays}},
		"custom_data":     p.CustomData,
	}
	items := make([]item, 0, len(p.Items))
	for _, it := range p.Items {
		if it.AmountMinor < 0 {
			return "", fmt.Errorf("paddle item %q has a negative amount", it.Description)
		}
		description := it.Description
		if len(description) < 2 {
			description = "Usage"
		}
		items = append(items, item{Quantity: 1, Price: price{Description: safetext.Truncate(description, 500),
			UnitPrice: map[string]string{"amount": strconv.FormatInt(it.AmountMinor, 10), "currency_code": strings.ToUpper(p.Currency)},
			Product:   map[string]string{"name": "API usage", "tax_category": "standard"}}})
	}
	body["items"] = items
	var out struct {
		Data transactionData `json:"data"`
	}
	if err := c.do(ctx, key, http.MethodPost, "/transactions", body, &out); err != nil {
		return "", err
	}
	return out.Data.ID, nil
}

// TransactionState is a transaction's payment state (ADR-1504).
type TransactionState struct {
	Status      string
	Currency    string
	GrandTotal  int64
	Balance     int64
	BilledAt    *time.Time
	DueAt       *time.Time
	PaidAt      *time.Time
	CheckoutURL string
	Deleted     bool
}

// GetTransaction reads a transaction's payment state.
func (c *Client) GetTransaction(ctx context.Context, key, transactionID string) (TransactionState, error) {
	var out struct {
		Data transactionData `json:"data"`
	}
	err := c.do(ctx, key, http.MethodGet, "/transactions/"+url.PathEscape(transactionID), nil, &out)
	var paddleErr *Error
	if errors.As(err, &paddleErr) && paddleErr.Status == http.StatusNotFound {
		return TransactionState{Deleted: true}, nil
	}
	if err != nil {
		return TransactionState{}, err
	}
	t := out.Data
	st := TransactionState{Status: t.Status, Currency: t.CurrencyCode, BilledAt: t.BilledAt}
	st.GrandTotal, _ = strconv.ParseInt(t.Details.Totals.GrandTotal, 10, 64)
	st.Balance, _ = strconv.ParseInt(t.Details.Totals.Balance, 10, 64)
	if t.Checkout != nil {
		st.CheckoutURL = t.Checkout.URL
	}
	if t.BilledAt != nil && t.BillingDetails != nil {
		st.DueAt = addTerms(*t.BilledAt, t.BillingDetails.PaymentTerms.Interval, t.BillingDetails.PaymentTerms.Frequency)
	}
	for _, p := range t.Payments {
		if p.CapturedAt != nil {
			at := p.CapturedAt.UTC()
			st.PaidAt = &at
			break
		}
	}
	return st, nil
}

func addTerms(from time.Time, interval string, n int) *time.Time {
	var due time.Time
	switch interval {
	case "day":
		due = from.AddDate(0, 0, n)
	case "week":
		due = from.AddDate(0, 0, 7*n)
	case "month":
		due = from.AddDate(0, n, 0)
	case "year":
		due = from.AddDate(n, 0, 0)
	default:
		return nil
	}
	due = due.UTC()
	return &due
}
