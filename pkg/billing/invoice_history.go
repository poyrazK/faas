package billing

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var ErrInvoiceHistoryCursor = errors.New("billing: invalid invoice history cursor")

// InvoiceHistoryReader enumerates one bounded page for the authenticated provider customer.
type InvoiceHistoryReader interface {
	FetchInvoiceHistory(ctx context.Context, acct state.Account, cursor string, limit int) (InvoiceHistoryPage, error)
}

// InvoiceHistoryPage reports provider records scanned and valid records available for import.
type InvoiceHistoryPage struct {
	Invoices   []state.Invoice
	Scanned    int
	NextCursor string
	HasMore    bool
}

type invoiceHistoryCursor struct {
	Version  int    `json:"v"`
	Provider string `json:"p"`
	Customer string `json:"c"`
	Position string `json:"x"`
}

func EncodeInvoiceHistoryCursor(provider, customer, position string) string {
	if provider == "" || customer == "" || position == "" {
		return ""
	}
	fingerprint := sha256.Sum256([]byte(customer))
	raw, _ := json.Marshal(invoiceHistoryCursor{Version: 1, Provider: provider, Customer: base64.RawURLEncoding.EncodeToString(fingerprint[:12]), Position: position})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeInvoiceHistoryCursor(cursor, provider, customer string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	if len(cursor) > 2048 {
		return "", ErrInvoiceHistoryCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", ErrInvoiceHistoryCursor
	}
	var decoded invoiceHistoryCursor
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Version != 1 || decoded.Provider != provider || decoded.Position == "" || len(decoded.Position) > 512 {
		return "", ErrInvoiceHistoryCursor
	}
	fingerprint := sha256.Sum256([]byte(customer))
	if decoded.Customer != base64.RawURLEncoding.EncodeToString(fingerprint[:12]) {
		return "", ErrInvoiceHistoryCursor
	}
	return decoded.Position, nil
}

func ValidateInvoiceHistoryLimit(limit int) error {
	if limit < 1 || limit > api.MaxInvoiceHistoryPageSize {
		return fmt.Errorf("billing: invoice history page size must be between 1 and %d", api.MaxInvoiceHistoryPageSize)
	}
	return nil
}

func ValidateInvoiceHistoryPage(page InvoiceHistoryPage, provider string, limit int) error {
	if page.Scanned < 0 || page.Scanned > limit || len(page.Invoices) > page.Scanned || len(page.Invoices) > limit {
		return errors.New("billing: invalid invoice history page size")
	}
	if page.HasMore != (page.NextCursor != "") || (page.HasMore && page.Scanned == 0) {
		return errors.New("billing: invalid invoice history pagination")
	}
	for _, inv := range page.Invoices {
		if inv.Provider != provider || inv.Plan != state.InvoicePlanUnknown || strings.TrimSpace(inv.ProviderInvoiceID) == "" {
			return errors.New("billing: invalid invoice history record")
		}
	}
	return nil
}
