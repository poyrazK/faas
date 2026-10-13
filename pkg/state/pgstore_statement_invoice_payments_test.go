//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1302 — completed exports are claimed for payment syncs, recorded
// statuses drive the unpaid/overdue filters and the once-only digest, and a
// final status stops syncing.
func TestPgStatementInvoicePaymentLifecycle(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	export, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: accountID,
		SourceKind: state.StatementSourceAppConsumer, StatementID: uuid.NewString(), Provider: state.BillingProviderStripe,
		Customer: "cus_Pg1", Currency: "EUR", AmountMinor: 375})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatementInvoiceExportInvoice(ctx, accountID, export.ID, "in_pg1"); err != nil {
		t.Fatal(err)
	}
	done, err := store.CompleteStatementInvoiceExport(ctx, accountID, export.ID)
	if err != nil || done.PaymentStatus != state.InvoicePaymentDraft || done.NextStatusSyncAt == nil {
		t.Fatalf("completed export = %+v, %v", done, err)
	}
	now := done.NextStatusSyncAt.Add(time.Minute)
	if early, err := store.ClaimStatementInvoiceSyncs(ctx, done.NextStatusSyncAt.Add(-time.Minute), time.Minute, 10); err != nil || len(early) != 0 {
		t.Fatalf("claim before due = %+v, %v", early, err)
	}
	claimed, err := store.ClaimStatementInvoiceSyncs(ctx, now, 5*time.Minute, 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != export.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if again, err := store.ClaimStatementInvoiceSyncs(ctx, now, 5*time.Minute, 10); err != nil || len(again) != 0 {
		t.Fatalf("claim during lease = %+v, %v", again, err)
	}

	due := now.AddDate(0, 0, -3)
	next := now.Add(time.Hour)
	prev, err := store.RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{Status: state.InvoicePaymentOpen,
		AmountRemainingMinor: 375, DueAt: &due, HostedInvoiceURL: "https://invoice.stripe.com/i/pg1"}, now, &next)
	if err != nil || prev != state.InvoicePaymentDraft {
		t.Fatalf("record open: prev=%q err=%v", prev, err)
	}
	for filter, want := range map[string]int{state.InvoiceFilterUnpaid: 1, state.InvoiceFilterOverdue: 1, state.InvoicePaymentPaid: 0, "": 1} {
		if got, err := store.ListStatementInvoices(ctx, accountID, filter, now, "", 10); err != nil || len(got) != want {
			t.Fatalf("list %q = %d invoices, %v; want %d", filter, len(got), err, want)
		}
	}
	overdue, err := store.ListOverdueStatementInvoices(ctx, now, 10)
	if err != nil || len(overdue) != 1 || !overdue[0].Overdue(now) {
		t.Fatalf("overdue = %+v, %v", overdue, err)
	}
	if err := store.MarkStatementInvoicesOverdueNotified(ctx, []string{export.ID}, now); err != nil {
		t.Fatal(err)
	}
	if overdue, err := store.ListOverdueStatementInvoices(ctx, now, 10); err != nil || len(overdue) != 0 {
		t.Fatalf("overdue after digest = %+v, %v", overdue, err)
	}

	paidAt := now.Add(time.Hour)
	if prev, err := store.RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{Status: state.InvoicePaymentPaid,
		AmountPaidMinor: 375, DueAt: &due, PaidAt: &paidAt}, paidAt, nil); err != nil || prev != state.InvoicePaymentOpen {
		t.Fatalf("record paid: prev=%q err=%v", prev, err)
	}
	if claimed, err := store.ClaimStatementInvoiceSyncs(ctx, paidAt.AddDate(1, 0, 0), time.Minute, 10); err != nil || len(claimed) != 0 {
		t.Fatalf("paid invoice still syncing: %+v, %v", claimed, err)
	}
	page, err := store.ListStatementInvoices(ctx, accountID, state.InvoicePaymentPaid, paidAt, "", 1)
	if err != nil || len(page) != 1 || page[0].PaidAt == nil {
		t.Fatalf("paid list = %+v, %v", page, err)
	}
	if rest, err := store.ListStatementInvoices(ctx, accountID, "", paidAt, state.InvoiceCursor(page[0]), 10); err != nil || len(rest) != 0 {
		t.Fatalf("page after the only invoice = %+v, %v", rest, err)
	}
	if err := store.DeferStatementInvoiceSync(ctx, uuid.NewString(), now); err == nil {
		t.Fatal("deferring an unknown export succeeded")
	}
}
