package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// InvoiceDetailsReader fetches facts for a known document, without moving money.
// Implementations verify the provider document and customer against the input.
type InvoiceDetailsReader interface {
	FetchInvoiceDetails(context.Context, state.Account, state.Invoice) (*state.InvoiceDetails, error)
}

var ErrInvoiceSourceMismatch = errors.New("billing: invoice source disagrees with stored identity or amounts")

type InvoiceRefreshLimitError struct {
	Kind     string
	Limit    int
	Observed int
}

func (e *InvoiceRefreshLimitError) Error() string {
	return fmt.Sprintf("invoice refresh exceeds %d %s", e.Limit, e.Kind)
}

// ValidateInvoiceSource pins authenticated document facts to the local snapshot.
func ValidateInvoiceSource(acct state.Account, inv state.Invoice, customer, document, currency string, total, tax int64) error {
	if acct.ID == "" || inv.AccountID != acct.ID || acct.ProviderCustomerID == "" || customer != acct.ProviderCustomerID ||
		document == "" || document != inv.ProviderInvoiceID || !strings.EqualFold(currency, inv.Currency) || currency == "" ||
		total != inv.TotalCents || tax != inv.TaxCents || total < 0 || tax < 0 || tax > total {
		return ErrInvoiceSourceMismatch
	}
	return nil
}

// InvoiceFactsMap retains integer precision when adapting SDK objects or raw JSON.
func InvoiceFactsMap(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var data map[string]any
	if err := dec.Decode(&data); err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("billing: missing invoice object")
	}
	return data, nil
}

func ValidateRefreshedDetails(details *state.InvoiceDetails) error {
	if details == nil {
		return errors.New("billing: missing invoice facts")
	}
	if details.Lines != nil && len(details.Lines.Items) > api.MaxInvoiceLineItems {
		return ValidateInvoiceLineCount(len(details.Lines.Items))
	}
	return state.ValidateInvoiceDetails(details)
}

// ValidateInvoiceLineCount bounds raw lists before normalization can omit
// invalid or repeated entries from the persisted snapshot.
func ValidateInvoiceLineCount(count int) error {
	if count > api.MaxInvoiceLineItems {
		return &InvoiceRefreshLimitError{Kind: "line items", Limit: api.MaxInvoiceLineItems, Observed: count}
	}
	return nil
}
