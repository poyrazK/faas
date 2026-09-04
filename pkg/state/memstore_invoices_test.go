package state

import (
	"context"
	"testing"
	"time"
)

// seedInvoice injects a row directly into the memstore's invoice map
// so the tests can exercise ListInvoicesForAccount without going
// through the production upsert path (PR B wires UpsertInvoice).
func seedInvoice(t *testing.T, m *MemStore, accountID, provider, providerInvoiceID string, periodEnd time.Time, totalCents int64) Invoice {
	t.Helper()
	inv := Invoice{
		ID:                "inv-" + providerInvoiceID,
		AccountID:         accountID,
		Provider:          provider,
		ProviderInvoiceID: providerInvoiceID,
		Status:            "paid",
		PeriodStart:       periodEnd.AddDate(0, 0, -30),
		PeriodEnd:         periodEnd,
		TotalCents:        totalCents,
		Currency:          "eur",
		PDFAvailable:      true,
	}
	m.mu.Lock()
	m.invoices[inv.ID] = inv
	m.mu.Unlock()
	return inv
}

func TestMemStore_ListInvoicesForAccount_OrderingAndScope(t *testing.T) {
	m := NewMemStore()
	seedInvoice(t, m, "alice", "stripe", "in_old", time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC), 900)
	seedInvoice(t, m, "alice", "stripe", "in_new", time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC), 900)
	seedInvoice(t, m, "bob", "stripe", "in_other", time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC), 100)

	rows, err := m.ListInvoicesForAccount(context.Background(), "alice", nil, time.Time{}, 25)
	if err != nil {
		t.Fatalf("ListInvoicesForAccount: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (account scope broken)", len(rows))
	}
	if !rows[0].PeriodEnd.After(rows[1].PeriodEnd) {
		t.Fatalf("ordering broken: first=%v second=%v", rows[0].PeriodEnd, rows[1].PeriodEnd)
	}
}

func TestMemStore_ListInvoicesForAccount_MonthFilter(t *testing.T) {
	m := NewMemStore()
	seedInvoice(t, m, "alice", "stripe", "in_jul", time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC), 900)
	seedInvoice(t, m, "alice", "stripe", "in_aug", time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC), 900)

	july := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	rows, err := m.ListInvoicesForAccount(context.Background(), "alice", &july, time.Time{}, 25)
	if err != nil {
		t.Fatalf("ListInvoicesForAccount: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("month filter broken: got %d rows, want 1", len(rows))
	}
	if rows[0].ProviderInvoiceID != "in_jul" {
		t.Fatalf("month filter returned %q, want in_jul", rows[0].ProviderInvoiceID)
	}
}

func TestMemStore_ListInvoicesForAccount_Empty(t *testing.T) {
	m := NewMemStore()
	rows, err := m.ListInvoicesForAccount(context.Background(), "alice", nil, time.Time{}, 25)
	if err != nil {
		t.Fatalf("ListInvoicesForAccount: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

func TestMemStore_UpsertInvoiceIsIdempotentAndUpdatesStatus(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	inv := Invoice{
		AccountID: "alice", Provider: "polar", ProviderInvoiceID: "order-1",
		Status: "open", PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0),
		TotalCents: 1000, Currency: "eur",
	}
	if err := m.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	inv.Status = "paid"
	inv.AmountPaidCents = 1000
	if err := m.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ListInvoicesForAccount(ctx, "alice", nil, time.Time{}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "paid" || rows[0].AmountPaidCents != 1000 {
		t.Fatalf("invoices after upsert = %+v, want one paid row", rows)
	}
}
