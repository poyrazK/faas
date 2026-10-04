package state_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/focus"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr:382
func TestMemInvoiceLifecycle(t *testing.T) {
	s := state.NewMemStore()
	testInvoiceLifecycle(t, s)
	testInvoiceLifecycleConcurrency(t, s)
	testInvoiceLifecycleHistory(t, s, func(inv state.Invoice) { s.SeedInvoiceForTest(inv) })
}

// adr:382
func TestPgInvoiceLifecycle(t *testing.T) {
	s, pool, ctx := pgStoreInvoicesWithPool(t)
	testInvoiceLifecycle(t, s)
	testInvoiceLifecycleConcurrency(t, s)
	testInvoiceLifecycleHistory(t, s, func(inv state.Invoice) {
		encoded := []byte("{}")
		if inv.Lifecycle != nil {
			var err error
			encoded, err = json.Marshal(inv.Lifecycle)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := pool.Exec(ctx, `UPDATE invoices SET detail_lifecycle = $2 WHERE id = $1`, inv.ID, encoded); err != nil {
			t.Fatal(err)
		}
	})
	for _, value := range []string{"[]", `{"records":[]}`, `{"records":null}`} {
		if _, err := pool.Exec(ctx, `UPDATE invoices SET detail_lifecycle = $1`, []byte(value)); err == nil {
			t.Fatalf("database accepted invalid lifecycle: %s", value)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices SET detail_lifecycle = jsonb_build_object('records',
		(SELECT jsonb_object_agg(n::text, '{}'::jsonb) FROM generate_series(1,$1::int) AS n))`, api.MaxInvoiceLifecycleRecords+1); err == nil {
		t.Fatal("database accepted unbounded lifecycle history")
	}
}

func lifecycleFixture(t *testing.T, s state.Store, suffix string) state.Invoice {
	t.Helper()
	acct, err := s.CreateAccount(context.Background(), "lifecycle-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	return state.Invoice{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: "order-" + suffix,
		Status: "paid", Currency: "eur", Plan: api.PlanPro, TotalCents: 1000,
		PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		Details: &state.InvoiceDetails{IssuerName: "Invoice seller", PaymentTerms: "Net 30", IssuedAt: "2026-09-30T00:00:00Z", DueAt: "2026-10-30T00:00:00Z",
			Lines: &state.InvoiceLines{Complete: true, Items: []state.InvoiceLineItem{{ID: "plan", Description: "Plan", ChargeCategory: "Purchase", NetCents: 1000}}}}}
}

func deliverLifecycle(t *testing.T, s state.Store, inv state.Invoice) state.Invoice {
	t.Helper()
	// PostgreSQL timestamps have microsecond precision. Separate deliveries so
	// assertions distinguish a changed record from an unchanged replay.
	time.Sleep(time.Millisecond)
	if err := s.UpsertInvoice(context.Background(), inv); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetInvoiceByProviderID(context.Background(), inv.AccountID, inv.Provider, inv.ProviderInvoiceID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func lifecycleExport(t *testing.T, inv state.Invoice) focus.Metadata {
	t.Helper()
	month, _ := focus.ParseMonth("2026-09")
	d, err := focus.BuildInvoiceDetail(inv.AccountID, month, time.Now().UTC(), []state.Invoice{inv})
	if err != nil {
		t.Fatal(err)
	}
	var m focus.Metadata
	if err := json.Unmarshal(d.Metadata, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func lifecycleRecords(t *testing.T, inv state.Invoice) map[string]state.InvoiceRecordLifecycle {
	t.Helper()
	m := lifecycleExport(t, inv)
	if m.Projection.SourceCoverage.UntrackedLifecycleRecords != 0 || m.Projection.SourceCoverage.LegacyLifecycleRecords != 0 {
		t.Fatalf("new invoice has a false history gap: %+v", m.Projection.SourceCoverage)
	}
	out := make(map[string]state.InvoiceRecordLifecycle)
	for _, record := range state.InvoiceExportRecords(inv) {
		history, ok := inv.Lifecycle.Records[record.ID()]
		if !ok || history.Fingerprint != record.Fingerprint() {
			t.Fatal("export and stored history disagree")
		}
		out[record.Values()[5]] = history
	}
	return out
}

func assertLifecycleChange(t *testing.T, before, after state.InvoiceRecordLifecycle, changed bool) {
	t.Helper()
	if before.CreatedAt != after.CreatedAt {
		t.Fatal("existing record creation date changed")
	}
	if !changed {
		if before != after {
			t.Fatal("unchanged exported values advanced lifecycle")
		}
		return
	}
	b, _ := time.Parse(time.RFC3339Nano, before.UpdatedAt)
	a, _ := time.Parse(time.RFC3339Nano, after.UpdatedAt)
	if !a.After(b) || before.Fingerprint == after.Fingerprint {
		t.Fatal("changed exported values did not advance lifecycle")
	}
}

func testInvoiceLifecycle(t *testing.T, s state.Store) {
	t.Helper()
	inv := deliverLifecycle(t, s, lifecycleFixture(t, s, "components"))
	first := lifecycleRecords(t, inv)["Purchase"]
	if inv.Lifecycle.LegacyHistory || first.CreatedAt != first.UpdatedAt {
		t.Fatal("new record lifecycle is incorrect")
	}
	// Neither caller-supplied history nor non-exported fields can change dates.
	inv.Lifecycle = &state.InvoiceLifecycle{StartedAt: "forged"}
	inv.Number, inv.PDFAvailable, inv.AmountPaidCents, inv.Plan, inv.Status = "new number", true, 1000, api.PlanHobby, "open"
	inv = deliverLifecycle(t, s, inv)
	assertLifecycleChange(t, first, lifecycleRecords(t, inv)["Purchase"], false)
	if inv.Plan != api.PlanPro {
		t.Fatal("historical invoice plan changed")
	}
	inv.Details = nil
	inv = deliverLifecycle(t, s, inv)
	assertLifecycleChange(t, first, lifecycleRecords(t, inv)["Purchase"], false)
	// A newly appearing tax component gets its own creation instant, while
	// the charge record remains byte-for-byte unchanged apart from source facts.
	inv.TaxCents, inv.TotalCents, inv.Details.Lines.Items[0].TaxCents = 190, 1190, 190
	inv = deliverLifecycle(t, s, inv)
	withTax := lifecycleRecords(t, inv)
	assertLifecycleChange(t, first, withTax["Purchase"], false)
	if withTax["Tax"].CreatedAt == first.CreatedAt || withTax["Tax"].CreatedAt != withTax["Tax"].UpdatedAt {
		t.Fatal("tax creation reused the charge creation date")
	}
	inv.TotalCents, inv.Details.Lines.Items[0].NetCents = 1090, 900
	inv = deliverLifecycle(t, s, inv)
	changedNet := lifecycleRecords(t, inv)
	assertLifecycleChange(t, withTax["Purchase"], changedNet["Purchase"], true)
	assertLifecycleChange(t, withTax["Tax"], changedNet["Tax"], false)
	inv.Details.Lines.Items[0].ChargeCategory = "Usage"
	inv = deliverLifecycle(t, s, inv)
	changedCategory := lifecycleRecords(t, inv)
	assertLifecycleChange(t, changedNet["Purchase"], changedCategory["Usage"], true)
	assertLifecycleChange(t, changedNet["Tax"], changedCategory["Tax"], false)
	// Each shared field must participate in both record fingerprints.
	for _, change := range []struct {
		name  string
		apply func(*state.Invoice)
	}{
		{"description", func(i *state.Invoice) { i.Details.Lines.Items[0].Description = "Corrected plan" }},
		{"issuer", func(i *state.Invoice) { i.Details.IssuerName = "Legal issuer" }},
		{"terms", func(i *state.Invoice) { i.Details.PaymentTerms = "Net 45" }},
		{"issue date", func(i *state.Invoice) { i.Details.IssuedAt = "2026-09-29T00:00:00Z" }},
		{"due date", func(i *state.Invoice) { i.Details.DueAt = "2026-11-15T00:00:00Z" }},
		{"period start", func(i *state.Invoice) { i.PeriodStart = i.PeriodStart.Add(time.Hour) }},
		{"period end", func(i *state.Invoice) { i.PeriodEnd = i.PeriodEnd.Add(time.Hour) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			before := lifecycleRecords(t, inv)
			change.apply(&inv)
			inv = deliverLifecycle(t, s, inv)
			after := lifecycleRecords(t, inv)
			assertLifecycleChange(t, before["Usage"], after["Usage"], true)
			assertLifecycleChange(t, before["Tax"], after["Tax"], true)
		})
	}
	before := lifecycleRecords(t, inv)
	inv.Currency, inv.Details.IssuedAt = "EUR", "2026-09-29T03:00:00+03:00"
	inv = deliverLifecycle(t, s, inv)
	if !reflect.DeepEqual(before, lifecycleRecords(t, inv)) {
		t.Fatal("equivalent normalized values changed dates")
	}
	// Read/seed history must not alias the stored map.
	for id := range inv.Lifecycle.Records {
		delete(inv.Lifecycle.Records, id)
	}
	stored, err := s.GetInvoiceByID(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Lifecycle.Records) != 2 {
		t.Fatal("returned lifecycle aliases stored history")
	}
	inv = stored
	// Removing tax retains its ID history; an identical return changes no column.
	inv.TaxCents, inv.TotalCents, inv.Details.Lines.Items[0].TaxCents = 0, 900, 0
	inv = deliverLifecycle(t, s, inv)
	assertLifecycleChange(t, before["Usage"], lifecycleRecords(t, inv)["Usage"], false)
	if len(inv.Lifecycle.Records) != 2 {
		t.Fatal("removed tax history was discarded")
	}
	inv.TaxCents, inv.TotalCents, inv.Details.Lines.Items[0].TaxCents = 190, 1090, 190
	inv = deliverLifecycle(t, s, inv)
	if !reflect.DeepEqual(before, lifecycleRecords(t, inv)) {
		t.Fatal("identical tax reappearance changed dates")
	}
	// Fallback rows have separate identities and lifetimes. Returning to either
	// representation preserves that representation's exact previous history.
	details := inv.Details
	inv.Details = &state.InvoiceDetails{Lines: &state.InvoiceLines{Complete: false}}
	inv = deliverLifecycle(t, s, inv)
	fallback := lifecycleRecords(t, inv)
	if len(inv.Lifecycle.Records) != 4 || fallback["Usage"].CreatedAt == before["Usage"].CreatedAt {
		t.Fatal("fallback history reused a provider record")
	}
	inv.Details = details
	inv = deliverLifecycle(t, s, inv)
	if !reflect.DeepEqual(before, lifecycleRecords(t, inv)) {
		t.Fatal("provider record reappearance changed history")
	}
	inv.Details = &state.InvoiceDetails{Lines: &state.InvoiceLines{Complete: false}}
	inv = deliverLifecycle(t, s, inv)
	if !reflect.DeepEqual(fallback, lifecycleRecords(t, inv)) {
		t.Fatal("aggregate replay changed history")
	}
	inv.Status = "void"
	inv = deliverLifecycle(t, s, inv)
	if lifecycleExport(t, inv).Projection.RowCount != 0 {
		t.Fatal("void invoice exported rows")
	}
	inv.Status = "paid"
	inv = deliverLifecycle(t, s, inv)
	if !reflect.DeepEqual(fallback, lifecycleRecords(t, inv)) {
		t.Fatal("status reappearance changed unchanged records")
	}
}

func testInvoiceLifecycleHistory(t *testing.T, s state.Store, seed func(state.Invoice)) {
	t.Helper()
	inv := deliverLifecycle(t, s, lifecycleFixture(t, s, "history"))
	inv.Lifecycle = nil // Simulate an invoice predating the additive migration.
	seed(inv)
	if lifecycleExport(t, inv).Projection.SourceCoverage.UntrackedLifecycleRecords != 1 {
		t.Fatal("untracked record was not declared")
	}
	inv = deliverLifecycle(t, s, inv)
	coverage := lifecycleExport(t, inv).Projection.SourceCoverage
	if !inv.Lifecycle.LegacyHistory || coverage.LegacyLifecycleRecords != 1 || coverage.UntrackedLifecycleRecords != 0 {
		t.Fatalf("legacy creation uncertainty lost: %+v", coverage)
	}
	// The history limit fails the whole write, preserving both source facts
	// and history. PostgreSQL must roll back its already-executed upsert.
	var sample state.InvoiceRecordLifecycle
	for _, record := range inv.Lifecycle.Records {
		sample = record
	}
	for n := len(inv.Lifecycle.Records); n < api.MaxInvoiceLifecycleRecords; n++ {
		inv.Lifecycle.Records[fmt.Sprintf("retired-%d", n)] = sample
	}
	seed(inv)
	before, err := s.GetInvoiceByID(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	inv.TaxCents, inv.TotalCents, inv.Details.Lines.Items[0].TaxCents, inv.Details.PaymentTerms = 190, 1190, 190, "Net 60"
	if err := s.UpsertInvoice(context.Background(), inv); err == nil {
		t.Fatal("history limit accepted a new record")
	}
	after, err := s.GetInvoiceByID(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed history write changed the invoice snapshot")
	}
}

func testInvoiceLifecycleConcurrency(t *testing.T, s state.Store) {
	t.Helper()
	inv := deliverLifecycle(t, s, lifecycleFixture(t, s, "concurrent"))
	original := lifecycleRecords(t, inv)["Purchase"].CreatedAt
	ctx := context.Background()
	month, _ := focus.ParseMonth("2026-09")
	var wg sync.WaitGroup
	errors := make(chan error, 9)
	start := make(chan struct{})
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			update := inv
			update.Details = &state.InvoiceDetails{PaymentTerms: fmt.Sprintf("Net %d", n+1)}
			errors <- s.UpsertInvoice(ctx, update)
		}(n)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for n := 0; n < 20; n++ {
			snapshot, err := s.GetInvoiceByID(ctx, inv.ID)
			if err == nil {
				_, err = focus.BuildInvoiceDetail(inv.AccountID, month, time.Now().UTC(), []state.Invoice{snapshot})
			}
			if err != nil {
				errors <- err
				return
			}
		}
		errors <- nil
	}()
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	final, err := s.GetInvoiceByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleRecords(t, final)["Purchase"].CreatedAt != original {
		t.Fatal("concurrent upserts changed creation date")
	}
}
