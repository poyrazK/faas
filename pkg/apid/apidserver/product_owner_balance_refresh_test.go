package apidserver_test

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlOwnerRefreshSelectedBalances(t *testing.T) {
	fixture := newStripeOwnerFixture(t)
	type invoice struct {
		scope  state.ProductBillingScope
		stripe *stripeDraftSend
		reads  atomic.Int32
	}
	staleAt := fixture.now.Add(-48 * time.Hour)
	seed := func(name, id string, month, due time.Time) *invoice {
		t.Helper()
		scope, stripe := fixture.draft(t, name, "cus_"+id, "in_"+id, id+"@example.com", month)
		if _, err := fixture.store.RecordStatementInvoicePayment(t.Context(), stripe.export, state.StatementInvoicePayment{
			Status: state.InvoicePaymentOpen, AmountRemainingMinor: 900, DueAt: &due,
		}, staleAt, nil); err != nil {
			t.Fatal(err)
		}
		unix := due.Unix()
		stripe.status, stripe.dueAt = "open", &unix
		return &invoice{scope: scope, stripe: stripe}
	}
	overdue := fixture.now.AddDate(0, 0, -2)
	paid := seed("Paid workspace", "paid", fixture.month.AddDate(0, -1, 0), overdue)
	partial := seed("Partial workspace", "partial", fixture.month, overdue)
	unavailable := seed("Unavailable workspace", "unavailable", fixture.month, overdue)
	current := seed("Current workspace", "current", fixture.month, fixture.now.AddDate(0, 0, 2))
	paid.stripe.status, paid.stripe.amountRemaining = "paid", 0
	partial.stripe.amountRemaining = 600

	invoices := map[string]*invoice{
		paid.stripe.invoiceID: paid, partial.stripe.invoiceID: partial,
		unavailable.stripe.invoiceID: unavailable, current.stripe.invoiceID: current,
	}
	var writes atomic.Int32
	provider := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			http.Error(w, "unexpected provider write", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/v1/invoices" {
			paid.stripe.ServeHTTP(w, r)
			return
		}
		id, ok := strings.CutPrefix(r.URL.Path, "/v1/invoices/")
		if inv := invoices[id]; ok && inv != nil {
			inv.reads.Add(1)
			if inv == unavailable {
				http.Error(w, "test provider outage", http.StatusServiceUnavailable)
				return
			}
			inv.stripe.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
	dashboard := fixture.dashboard(t, provider)
	const group = "/outstanding-balances?bucket=overdue&currency=EUR&mode=test"
	const refresh = "/payments/refresh-selected"
	value := func(inv *invoice) string { return inv.scope.TenantID + "/" + inv.stripe.statement }
	checkbox := func(inv *invoice) string {
		return `form="refresh-selected-balances" name="statement" value="` + value(inv) + `"`
	}
	page := dashboard.call(t, group, nil, http.StatusOK, "Refresh selected balances", "EUR 27.00",
		checkbox(paid), checkbox(partial), checkbox(unavailable))
	if strings.Contains(page, checkbox(current)) {
		t.Fatal("out-of-group invoice is selectable in the batch")
	}
	form := ownerConsoleForm(t, page, dashboard.base+refresh+"#outstanding-balances")
	selection := []string{value(paid), value(partial), value(unavailable)}
	tooMany := ownerConsoleCopy(form)
	tooMany["statement"] = slices.Repeat(selection, 2)
	dashboard.call(t, refresh, tooMany, http.StatusBadRequest)
	foreign := ownerConsoleCopy(form)
	foreign["statement"] = []string{value(paid), value(current)}
	dashboard.call(t, refresh, foreign, http.StatusBadRequest)
	for _, inv := range invoices {
		if inv.reads.Load() != 0 {
			t.Fatal("an invalid selection reached the provider")
		}
	}

	form["statement"] = selection
	replayCookies := maps.Clone(dashboard.cookies)
	page = dashboard.call(t, refresh, form, http.StatusOK, "Payment refresh results", "Paid workspace", "Paid",
		"Balance updated", "EUR 6.00", "Needs refresh", "EUR 9.00", "EUR 15.00", "EUR 24.00",
		"Could not confirm payment status with Stripe.")
	if strings.Contains(page, checkbox(paid)) || !strings.Contains(page, checkbox(unavailable)) {
		t.Fatal("updated payment group did not remove the paid invoice or retain the unavailable invoice")
	}

	// Replay the original signed request, including a row that left the group.
	dashboard.cookies = replayCookies
	dashboard.call(t, refresh, form, http.StatusOK, "Payment refresh results", "Paid",
		"Balance updated", "Needs refresh", "invoice update is already in progress", "EUR 15.00")
	for _, inv := range []*invoice{paid, partial, unavailable} {
		if reads := inv.reads.Load(); reads != 1 {
			t.Fatalf("%s provider reads = %d, want 1", inv.stripe.invoiceID, reads)
		}
	}
	if current.reads.Load() != 0 || writes.Load() != 0 {
		t.Fatalf("out-of-group reads = %d, provider writes = %d", current.reads.Load(), writes.Load())
	}
	failedExport, err := fixture.store.GetStatementInvoiceExport(t.Context(), fixture.acct.ID, state.StatementSourceProductControl, unavailable.stripe.statement)
	if err != nil || failedExport.AmountRemainingMinor == nil || *failedExport.AmountRemainingMinor != 900 ||
		failedExport.StatusSyncedAt == nil || !failedExport.StatusSyncedAt.Equal(staleAt) {
		t.Fatalf("failed check replaced the last good observation: %+v, %v", failedExport, err)
	}
}
