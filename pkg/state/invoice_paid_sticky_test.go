package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261028101500
func TestMemInvoicePaidIsNotReopened(t *testing.T) {
	testInvoicePaidIsNotReopened(t, state.NewMemStore())
}

// adr: 20261028101500
func TestPgInvoicePaidIsNotReopened(t *testing.T) {
	s, _, _ := pgStoreInvoicesWithPool(t)
	testInvoicePaidIsNotReopened(t, s)
}

// A provider can deliver "order created" after "order paid". The late open
// snapshot must not reopen the paid invoice or zero its paid amount, while an
// unpaid order still moves forward to paid and a paid one can still be voided.
func testInvoicePaidIsNotReopened(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "invoice-sticky@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	period := time.Date(2026, 10, 27, 17, 36, 0, 0, time.UTC)
	upsert := func(id, status string, paid int64) state.Invoice {
		t.Helper()
		if err := s.UpsertInvoice(ctx, state.Invoice{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: id, Status: status,
			Currency: "usd", Plan: api.PlanHobby, PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0),
			SubtotalCents: 1000, TotalCents: 1089, TaxCents: 89, AmountPaidCents: paid}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetInvoiceByProviderID(ctx, acct.ID, "polar", id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	tests := []struct {
		name     string
		id       string
		steps    []string
		want     string
		wantPaid int64
	}{
		{"late created after paid", "order-late", []string{"paid", "open"}, "paid", 1089},
		{"late draft after paid", "order-draft", []string{"paid", "draft"}, "paid", 1089},
		{"open then paid", "order-forward", []string{"open", "paid"}, "paid", 1089},
		{"paid then void", "order-void", []string{"paid", "void"}, "void", 0},
		{"stays open while unpaid", "order-open", []string{"open", "open"}, "open", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got state.Invoice
			for _, status := range tt.steps {
				paid := int64(0)
				if status == "paid" {
					paid = 1089
				}
				got = upsert(tt.id, status, paid)
			}
			if got.Status != tt.want || got.AmountPaidCents != tt.wantPaid {
				t.Fatalf("after %v: status %q paid %d, want %q paid %d", tt.steps, got.Status, got.AmountPaidCents, tt.want, tt.wantPaid)
			}
		})
	}
}
