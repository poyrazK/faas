package state_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr:381
func TestMemInvoiceDetailsPersistence(t *testing.T) {
	testInvoiceDetailsPersistence(t, state.NewMemStore())
}

// adr:381
func TestPgInvoiceDetailsPersistence(t *testing.T) {
	s, pool, ctx := pgStoreInvoicesWithPool(t)
	testInvoiceDetailsPersistence(t, s)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET details = '[]'::jsonb`); err == nil {
		t.Fatal("database accepted a non-object invoice snapshot")
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices SET details = jsonb_build_object('line_first_seen',
		(SELECT jsonb_object_agg(n::text, '2026-09-30T00:00:00Z'::text) FROM generate_series(1,$1::int) AS n))`, api.MaxInvoiceSeenLineIDs+1); err == nil {
		t.Fatal("database accepted unbounded historical line IDs")
	}
}

func testInvoiceDetailsPersistence(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "invoice-details@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	inv := state.Invoice{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: "order-1", Status: "paid", Currency: "eur", Plan: api.PlanPro,
		PeriodStart: period, PeriodEnd: period.AddDate(0, 0, 29), TotalCents: 1190, TaxCents: 190,
		Details: &state.InvoiceDetails{IssuerName: "Invoice seller", PaymentTerms: "Net 30", IssuedAt: "2026-09-30T00:00:00Z", DueAt: "2026-10-30T00:00:00Z",
			Lines: &state.InvoiceLines{Complete: true, Items: []state.InvoiceLineItem{{ID: "item-1", Description: "Plan", ChargeCategory: "Purchase", NetCents: 1000, TaxCents: 190}}}}}
	if err := s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetInvoiceByProviderID(ctx, acct.ID, "polar", "order-1")
	if err != nil {
		t.Fatal(err)
	}
	if gap := state.InvoiceLineGap(first); gap != "" {
		t.Fatalf("line gap=%s", gap)
	}
	line := first.Details.Lines.Items[0]
	if line.CreatedAt == "" || line.UpdatedAt == "" {
		t.Fatal("line ingestion dates missing")
	}
	// Inputs and reads cannot mutate the stored slice or facts.
	inv.Details.Lines.Items[0].Description = "Mutated input"
	first.Details.PaymentTerms = "Mutated output"
	first.Details.Lines.Items[0].Description = "Mutated output"
	stored, err := s.GetInvoiceByID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Details.PaymentTerms != "Net 30" || stored.Details.Lines.Items[0].Description != "Plan" {
		t.Fatal("stored details alias caller memory")
	}
	inv.Details = nil // A sparse paid/replayed event must preserve rich facts.
	inv.Plan = api.PlanHobby
	if err := s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.GetInvoiceByID(ctx, first.ID)
	if stored.Details.PaymentTerms != "Net 30" || stored.Plan != api.PlanPro || stored.Details.Lines.Items[0] != line {
		t.Fatal("sparse event erased facts, line dates, or historical plan")
	}
	// Exact replay preserves a detail's creation/update instant.
	inv.Details = stored.Details
	if err := s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.GetInvoiceByID(ctx, first.ID)
	if stored.Details.Lines.Items[0] != line {
		t.Fatal("replay changed line timestamps")
	}
	// A changed line retains first ingestion; a newly observed ID gets its own.
	inv.Details = &state.InvoiceDetails{Lines: &state.InvoiceLines{Complete: true, Items: []state.InvoiceLineItem{
		{ID: "item-1", Description: "Plan corrected", ChargeCategory: "Purchase", NetCents: 900, TaxCents: 190},
		{ID: "item-2", Description: "Usage", ChargeCategory: "Usage", NetCents: 100},
	}}}
	if err := s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListInvoicesForAccount(ctx, acct.ID, &period, time.Time{}, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	stored = rows[0]
	if state.InvoiceLineGap(stored) != "" || stored.Details.PaymentTerms != "Net 30" {
		t.Fatal("replacement snapshot failed reconciliation or scalar merge")
	}
	updated := stored.Details.Lines.Items[0]
	if updated.CreatedAt != line.CreatedAt || updated.UpdatedAt == line.UpdatedAt {
		t.Fatal("changed line dates are incorrect")
	}
	// An incomplete replacement must invalidate completeness, even if it has
	// the same amounts. Empty arrays are legal sparse source snapshots.
	inv.Details = &state.InvoiceDetails{Lines: &state.InvoiceLines{Complete: false}}
	if err := s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.GetInvoiceByID(ctx, first.ID)
	if state.InvoiceLineGap(stored) != "incomplete" {
		t.Fatal("incomplete snapshot kept a false completeness claim")
	}
	// Removing/re-observing a provider ID cannot change its first creation.
	inv.Details = &state.InvoiceDetails{Lines: &state.InvoiceLines{Complete: true, Items: []state.InvoiceLineItem{
		{ID: "item-1", Description: "Plan restored", ChargeCategory: "Purchase", NetCents: 1000, TaxCents: 190},
	}}}
	if err := s.UpsertInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.GetInvoiceByID(ctx, first.ID)
	if stored.Details.Lines.Items[0].CreatedAt != line.CreatedAt {
		t.Fatal("re-observing an ID changed its creation date")
	}
	rows, _ = s.ListInvoicesForAccount(ctx, acct.ID, nil, time.Time{}, 10)
	rows[0].Details.LineFirstSeen["item-1"] = "mutated output"
	stored, _ = s.GetInvoiceByID(ctx, first.ID)
	if stored.Details.LineFirstSeen["item-1"] != line.CreatedAt {
		t.Fatal("historical line dates alias caller memory")
	}
	if _, err := s.GetInvoiceByProviderID(ctx, "00000000-0000-0000-0000-000000000000", "polar", "order-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("invoice crossed account boundary")
	}
}

// adr:381
func TestInvoiceDetailsValidationAndOverflow(t *testing.T) {
	for _, d := range []*state.InvoiceDetails{
		{PaymentTerms: strings.Repeat("x", api.MaxInvoiceDetailTextBytes+1)},
		{IssuedAt: "not-a-date"},
		{Lines: &state.InvoiceLines{Items: []state.InvoiceLineItem{{ID: "duplicate"}, {ID: "duplicate"}}}},
		{Lines: &state.InvoiceLines{Items: []state.InvoiceLineItem{{ID: "line", ChargeCategory: "unknown"}}}},
		{Lines: &state.InvoiceLines{Items: make([]state.InvoiceLineItem, api.MaxInvoiceLineItems+1)}},
	} {
		if state.ValidateInvoiceDetails(d) == nil {
			t.Fatalf("accepted invalid details: %+v", d)
		}
	}
	inv := state.Invoice{TotalCents: 10, Details: &state.InvoiceDetails{Lines: &state.InvoiceLines{Complete: true, Items: []state.InvoiceLineItem{
		{ID: "1", ChargeCategory: "Purchase", NetCents: math.MaxInt64},
		{ID: "2", ChargeCategory: "Usage", NetCents: math.MaxInt64},
	}}}}
	if state.InvoiceLineGap(inv) != "totals_mismatch" {
		t.Fatal("overflow passed reconciliation")
	}
}
