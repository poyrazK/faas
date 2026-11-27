package product

import (
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

var ownerBalanceFormFields = [...]string{"balance_mode", "balance_currency", "balance_bucket", "balance_cursor"}

type ownerBalanceSelection struct {
	Mode     string `json:"mode"`
	Currency string `json:"currency,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

func ownerBalanceFromForm(form url.Values) *ownerBalanceSelection {
	for _, key := range ownerBalanceFormFields {
		if form.Has(key) {
			return &ownerBalanceSelection{
				Mode: form.Get("balance_mode"), Currency: form.Get("balance_currency"),
				Bucket: form.Get("balance_bucket"), Cursor: form.Get("balance_cursor"),
			}
		}
	}
	return nil
}

func (b ownerBalanceSelection) filter() state.ProductOwnerBalanceFilter {
	return state.ProductOwnerBalanceFilter{
		Livemode: b.Mode == "live", Currency: b.Currency, Bucket: b.Bucket, Cursor: b.Cursor,
	}
}

func (b ownerBalanceSelection) valid() bool {
	return (b.Mode == "live" || b.Mode == "test") && b.filter().Valid()
}

func (b ownerBalanceSelection) validDrilldown() bool {
	return b.Bucket != "" && b.valid()
}

func (b ownerBalanceSelection) url(c *ownerContext) string {
	query := url.Values{}
	if b.Mode == "test" {
		query.Set("mode", b.Mode)
	}
	if b.Bucket != "" {
		query.Set("currency", b.Currency)
		query.Set("bucket", b.Bucket)
	}
	if b.Cursor != "" {
		query.Set("cursor", b.Cursor)
	}
	link := c.data.BaseURL + "/outstanding-balances"
	if len(query) > 0 {
		link += "?" + query.Encode()
	}
	return link
}

func (s *Service) OwnerOutstandingBalances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	query, err := ownerQuery(r, "mode", "currency", "bucket", "cursor")
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	selection := ownerBalanceSelection{
		Mode: query.Get("mode"), Currency: query.Get("currency"), Bucket: query.Get("bucket"), Cursor: query.Get("cursor"),
	}
	if !query.Has("mode") {
		selection.Mode = "live"
	}
	s.renderOwnerBalances(w, r, c, selection, "", nil)
}

func (s *Service) renderOwnerBalances(w http.ResponseWriter, r *http.Request, c *ownerContext, selection ownerBalanceSelection,
	notice string, problem *api.Problem) {
	if err := s.loadOwnerBalances(w, r, c, selection); err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	if queue := c.data.OutstandingBalances.Invoices; queue != nil {
		renderPaymentActionResult(w, queue, notice, problem)
	}
	s.OwnerRender(w, r, c.account, c.data)
}

func (s *Service) loadOwnerBalances(w http.ResponseWriter, r *http.Request, c *ownerContext, selection ownerBalanceSelection) error {
	if !selection.valid() {
		return state.ErrInvalidArgument
	}
	store, ok := s.Host.Store().(state.ProductOwnerOutstandingBalancesStore)
	if !ok {
		return state.ErrNotFound
	}
	now := s.clock()
	report, err := store.ProductOwnerOutstandingBalances(r.Context(), c.account.ID, c.app.ID, selection.filter(), now)
	if err != nil {
		return err
	}
	view := &dashboard.ProductControlOutstandingBalances{
		Mode: selection.Mode, Currency: selection.Currency, Bucket: selection.Bucket, Cursor: selection.Cursor,
		LiveURL:    (ownerBalanceSelection{Mode: "live"}).url(c),
		TestURL:    (ownerBalanceSelection{Mode: "test"}).url(c),
		SummaryURL: (ownerBalanceSelection{Mode: selection.Mode}).url(c),
	}
	view.Currencies = ownerBalanceCurrencies(c, selection.Mode, report.Totals)
	for _, bucket := range ownerBalanceBuckets {
		view.BucketLabels = append(view.BucketLabels, bucket.label)
		if selection.Bucket == bucket.id {
			view.Group = bucket.label
		}
	}
	if selection.Bucket != "" {
		c.data.ReviewToken, err = s.ownerToken(w, c, "review", c.account.ID+"/"+c.app.ID)
		if err != nil {
			return err
		}
		view.Invoices = &dashboard.ProductControlPaymentQueue{}
		for _, invoice := range report.Invoices {
			row := s.ownerPaymentRow(c, invoice.Payment, now)
			row.CanRefresh = s.RefreshInvoicePayment != nil
			row.NeedsRefresh = invoice.NeedsRefresh
			row.Action = "Open"
			if invoice.Status == state.InvoicePaymentUncollectible {
				row.Action = "Uncollectible"
			}
			view.Invoices.Items = append(view.Invoices.Items, row)
		}
		if err := s.prepareOwnerBalanceRefresh(w, c, view, selection, report.Invoices); err != nil {
			return err
		}
		if report.NextCursor != "" {
			selection.Cursor = report.NextCursor
			view.Invoices.NextURL = selection.url(c)
		}
	}
	c.data.OutstandingBalances = view
	return nil
}

var ownerBalanceBuckets = [...]struct{ id, label string }{
	{state.ProductOwnerBalanceCurrent, "Current"},
	{state.ProductOwnerBalanceOverdue, "1–30 days overdue"},
	{state.ProductOwnerBalanceOver30, "31+ days overdue"},
	{state.ProductOwnerBalanceNoDueDate, "No due date"},
	{state.ProductOwnerBalanceUncollectible, "Uncollectible"},
}

func ownerBalanceCurrencies(c *ownerContext, mode string, totals []state.ProductOwnerBalanceTotal) []dashboard.ProductControlBalanceCurrency {
	var currencies []dashboard.ProductControlBalanceCurrency
	for first := 0; first < len(totals); {
		currency := totals[first].Currency
		row := dashboard.ProductControlBalanceCurrency{Currency: currency, Buckets: make([]dashboard.ProductControlBalanceBucket, len(ownerBalanceBuckets))}
		var sum, amount big.Int
		for first < len(totals) && totals[first].Currency == currency {
			total := totals[first]
			first++
			row.InvoiceCount += total.InvoiceCount
			row.UnknownCount += total.UnknownCount
			if _, ok := amount.SetString(total.RemainingMinor, 10); ok {
				sum.Add(&sum, &amount)
			}
			for i, bucket := range ownerBalanceBuckets {
				if bucket.id == total.Bucket {
					row.Buckets[i] = dashboard.ProductControlBalanceBucket{
						Balance:      ownerOutstandingMinor(currency, total.RemainingMinor, total.InvoiceCount > total.UnknownCount),
						URL:          (ownerBalanceSelection{Mode: mode, Currency: currency, Bucket: bucket.id}).url(c),
						InvoiceCount: total.InvoiceCount, RefreshCount: total.RefreshCount, UnknownCount: total.UnknownCount,
					}
				}
			}
		}
		row.Total = ownerOutstandingMinor(currency, sum.String(), row.InvoiceCount > row.UnknownCount)
		currencies = append(currencies, row)
	}
	return currencies
}

func ownerOutstandingMinor(currency, digits string, known bool) string {
	if !known {
		return "Unknown"
	}
	exp, _ := api.CurrencyMinorExponent(currency)
	if exp == 0 {
		return currency + " " + digits
	}
	if len(digits) <= exp {
		digits = strings.Repeat("0", exp+1-len(digits)) + digits
	}
	point := len(digits) - exp
	return currency + " " + digits[:point] + "." + digits[point:]
}
