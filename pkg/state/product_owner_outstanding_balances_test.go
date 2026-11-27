package state

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func testProductOwnerOutstandingBalances(t *testing.T, s productOwnerBillingQueueTestStore, scope ProductBillingScope,
	newWorkspace func(string, int) ProductBillingScope) {
	t.Helper()
	ctx := t.Context()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	now := today.Add(12 * time.Hour)
	dueAt := func(days int) *time.Time {
		at := today.AddDate(0, 0, days)
		return &at
	}
	seed := func(name, currency, status string, live bool, monthOffset int, remaining int64, due *time.Time, observed time.Time) StatementInvoiceExport {
		t.Helper()
		workspace := newWorkspace(name, monthOffset)
		review, err := s.ReviewProductBilling(ctx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := s.FinalizeProductBilling(ctx, workspace, review.ReviewHash)
		if err != nil {
			t.Fatal(err)
		}
		export, _, err := s.BeginStatementInvoiceExport(ctx, StatementInvoiceExport{
			AccountID: scope.AccountID, SourceKind: StatementSourceProductControl, StatementID: doc.ID,
			Provider: BillingProviderStripe, Customer: "cus_balance", Currency: currency, AmountMinor: 900, Livemode: live,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetStatementInvoiceExportInvoice(ctx, scope.AccountID, export.ID, "in_balance_"+doc.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CompleteStatementInvoiceExport(ctx, scope.AccountID, export.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RecordStatementInvoicePayment(ctx, export.ID, StatementInvoicePayment{
			Status: status, AmountRemainingMinor: remaining, DueAt: due,
		}, observed, nil); err != nil {
			t.Fatal(err)
		}
		return export
	}
	const overdueCount = productOwnerBillingQueuePageSize + 2
	partial := seed("Balance oldest", "EUR", InvoicePaymentOpen, true, -1, 100, dueAt(-30), now)
	stale := seed("Balance stale", "EUR", InvoicePaymentOpen, true, 0, 100, dueAt(-2), now.Add(-api.ConsumerOverdueStatusMaxAge-time.Minute))
	for i := 2; i < overdueCount; i++ {
		seed(fmt.Sprintf("Balance %d", i), "EUR", InvoicePaymentOpen, true, 0, 100, dueAt(-2), now)
	}
	seed("Balance current", "USD", InvoicePaymentOpen, true, 0, 515, dueAt(0), now)
	seed("Balance older", "EUR", InvoicePaymentOpen, true, -1, 240, dueAt(-31), now)
	seed("Balance no date", "EUR", InvoicePaymentOpen, true, 0, 145, nil, now)
	seed("Balance uncollectible", "EUR", InvoicePaymentUncollectible, true, 0, 81, dueAt(-3), now)
	seed("Balance sandbox", "EUR", InvoicePaymentOpen, false, 0, 999, dueAt(-2), now)
	seed("Balance paid", "EUR", InvoicePaymentPaid, true, 0, 0, dueAt(-2), now)
	seed("Balance draft", "EUR", InvoicePaymentDraft, true, 0, 900, nil, now)

	list := func(filter ProductOwnerBalanceFilter, asOf time.Time) ProductOwnerOutstandingBalances {
		t.Helper()
		report, err := s.ProductOwnerOutstandingBalances(ctx, scope.AccountID, scope.AppID, filter, asOf)
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	bucket := func(report ProductOwnerOutstandingBalances, currency, group string) ProductOwnerBalanceTotal {
		t.Helper()
		for _, value := range report.Totals {
			if value.Currency == currency && value.Bucket == group {
				return value
			}
		}
		t.Fatalf("missing %s %s in %+v", currency, group, report.Totals)
		return ProductOwnerBalanceTotal{}
	}
	live := list(ProductOwnerBalanceFilter{Livemode: true}, now)
	if len(live.Totals) != 5 || len(live.Invoices) != 0 {
		t.Fatalf("live summary = %+v", live)
	}
	for _, want := range []struct{ currency, group, balance string }{
		{"USD", ProductOwnerBalanceCurrent, "515"}, {"EUR", ProductOwnerBalanceOver30, "240"},
		{"EUR", ProductOwnerBalanceNoDueDate, "145"}, {"EUR", ProductOwnerBalanceUncollectible, "81"},
		{"EUR", ProductOwnerBalanceOverdue, strconv.Itoa(overdueCount * 100)},
	} {
		if got := bucket(live, want.currency, want.group); got.RemainingMinor != want.balance {
			t.Fatalf("balance = %+v, want %s", got, want.balance)
		}
	}
	filter := ProductOwnerBalanceFilter{Livemode: true, Currency: "EUR", Bucket: ProductOwnerBalanceOverdue}
	first := list(filter, now)
	if len(first.Invoices) != productOwnerBillingQueuePageSize || first.NextCursor == "" || first.Invoices[0].Payment.DocumentID != partial.StatementID ||
		!reflect.DeepEqual(first.Totals, live.Totals) {
		t.Fatalf("first page = %+v", first)
	}
	if got := bucket(first, "EUR", ProductOwnerBalanceOverdue); got.InvoiceCount != overdueCount || got.RefreshCount != 1 {
		t.Fatalf("overdue totals = %+v", got)
	}
	filter.Cursor = first.NextCursor
	second := list(filter, now)
	if len(second.Invoices) != 2 || second.NextCursor != "" || !reflect.DeepEqual(second.Totals, live.Totals) {
		t.Fatalf("second page = %+v", second)
	}
	found := map[string]bool{}
	staleObserved := false
	for _, invoice := range append(first.Invoices, second.Invoices...) {
		id := invoice.Payment.DocumentID
		if found[id] {
			t.Fatalf("document %s appeared on both pages", id)
		}
		found[id] = true
		staleObserved = staleObserved || (id == stale.StatementID && invoice.NeedsRefresh)
	}
	if !staleObserved {
		t.Fatal("stale invoice did not carry its refresh flag")
	}
	filter.Livemode = false
	if _, err := s.ProductOwnerOutstandingBalances(ctx, scope.AccountID, scope.AppID, filter, now); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("live cursor accepted in test mode: %v", err)
	}
	test := list(ProductOwnerBalanceFilter{}, now)
	if len(test.Totals) != 1 || test.Totals[0].RemainingMinor != "999" || test.Totals[0].InvoiceCount != 1 {
		t.Fatalf("test summary = %+v", test)
	}
	if _, err := s.ProductOwnerOutstandingBalances(ctx, uuid.NewString(), scope.AppID, ProductOwnerBalanceFilter{Livemode: true}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account balances: %v", err)
	}

	now = now.Add(time.Second)
	if _, err := s.RecordStatementInvoicePayment(ctx, partial.ID, StatementInvoicePayment{
		Status: InvoicePaymentOpen, AmountRemainingMinor: 60, AmountPaidMinor: 40, DueAt: dueAt(-30),
	}, now, nil); err != nil {
		t.Fatal(err)
	}
	if got := bucket(list(ProductOwnerBalanceFilter{Livemode: true}, now), "EUR", ProductOwnerBalanceOverdue); got.RemainingMinor != strconv.Itoa(overdueCount*100-40) {
		t.Fatalf("partial payment totals = %+v", got)
	}
	now = now.Add(time.Second)
	if _, err := s.RecordStatementInvoicePayment(ctx, partial.ID, StatementInvoicePayment{
		Status: InvoicePaymentPaid, AmountPaidMinor: 100, DueAt: dueAt(-30),
	}, now, nil); err != nil {
		t.Fatal(err)
	}
	if got := bucket(list(ProductOwnerBalanceFilter{Livemode: true}, now), "EUR", ProductOwnerBalanceOverdue); got.InvoiceCount != overdueCount-1 ||
		got.RemainingMinor != strconv.Itoa((overdueCount-1)*100) {
		t.Fatalf("fully paid invoice remains in totals: %+v", got)
	}
}
