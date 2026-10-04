package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr:383
func TestMemInvoiceRefresh(t *testing.T) {
	s := state.NewMemStore()
	testInvoiceRefresh(t, s)
	testInvoiceRefreshHistoryLimit(t, s, s.SeedInvoiceForTest)
}

func TestMemInvoiceRefreshAdvancesWhenClockStalls(t *testing.T) {
	s := state.NewMemStore()
	ctx := context.Background()
	inv := deliverLifecycle(t, s, lifecycleFixture(t, s, "refresh-clock-stall"))
	inv.UpdatedAt = time.Now().UTC().Add(time.Minute)
	s.SeedInvoiceForTest(inv)

	updated, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, inv.UpdatedAt, &state.InvoiceDetails{PaymentTerms: "Net 30"})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.UpdatedAt.After(inv.UpdatedAt) {
		t.Fatalf("refresh revision = %s, want after %s", updated.UpdatedAt, inv.UpdatedAt)
	}
}

// adr:383
func TestPgInvoiceRefresh(t *testing.T) {
	s, pool, ctx := pgStoreInvoicesWithPool(t)
	testInvoiceRefresh(t, s)
	testInvoiceRefreshHistoryLimit(t, s, func(inv state.Invoice) {
		encoded, err := json.Marshal(inv.Lifecycle)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE invoices SET detail_lifecycle = $2 WHERE id = $1`, inv.ID, encoded); err != nil {
			t.Fatal(err)
		}
	})
	// Force the actual SQL enrichment write to fail, after its row lock and
	// lifecycle calculation, then verify the entire transaction was rolled back.
	inv := deliverLifecycle(t, s, lifecycleFixture(t, s, "refresh-rollback"))
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_refresh_for_test() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF NEW.details ->> 'payment_terms' = 'Blocked' THEN RAISE EXCEPTION 'test write failure'; END IF;
  RETURN NEW;
 END $$;
 CREATE TRIGGER reject_refresh_for_test BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION reject_refresh_for_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, inv.UpdatedAt, &state.InvoiceDetails{PaymentTerms: "Blocked"}); err == nil {
		t.Fatal("SQL write failure was ignored")
	}
	after, err := s.GetInvoiceByID(ctx, inv.ID)
	if err != nil || !refreshSnapshotsEqual(inv, after) {
		t.Fatalf("failed SQL write changed snapshot: %v", err)
	}
	if _, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, inv.UpdatedAt, &state.InvoiceDetails{PaymentTerms: "Net 60"}); err != nil {
		t.Fatalf("rollback left refresh unusable: %v", err)
	}
}

func testInvoiceRefresh(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	source := lifecycleFixture(t, s, "refresh")
	details := source.Details
	source.Details, source.AmountPaidCents = nil, source.TotalCents
	inv := deliverLifecycle(t, s, source)
	if err := s.RecordInvoiceRefund(ctx, state.InvoiceRefund{InvoiceID: inv.ID,
		ProviderRefundID: "refund-refresh", IdempotencyKey: "refund-refresh", Source: "credit", AmountCents: 100}); err != nil {
		t.Fatal(err)
	}
	inv, err := s.GetInvoiceByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, inv.UpdatedAt, details)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.UpdatedAt.After(inv.UpdatedAt) {
		t.Fatal("refresh did not advance its optimistic revision")
	}
	if state.InvoiceLineGap(updated) != "" || lifecycleExport(t, updated).Projection.SourceCoverage.DetailedInvoices != 1 {
		t.Fatal("refresh did not improve FOCUS coverage")
	}
	withoutFacts := func(i state.Invoice) state.Invoice {
		i.Details, i.Lifecycle, i.UpdatedAt = nil, nil, time.Time{}
		return i
	}
	if !reflect.DeepEqual(withoutFacts(inv), withoutFacts(updated)) {
		t.Fatal("refresh changed financial or payment fields")
	}
	if updated.AmountRefundedCents != 100 || updated.CreditsAppliedCents != 100 {
		t.Fatal("refund/credit history was lost")
	}
	if _, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, inv.UpdatedAt, &state.InvoiceDetails{PaymentTerms: "Stale"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale refresh error=%v", err)
	}
	if _, err := s.RefreshInvoiceDetails(ctx, "00000000-0000-0000-0000-000000000000", inv.ID, updated.UpdatedAt, details); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account refresh error=%v", err)
	}
	if _, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, updated.UpdatedAt, &state.InvoiceDetails{DueAt: "invalid"}); err == nil {
		t.Fatal("invalid refresh accepted")
	}
	stored, err := s.GetInvoiceByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshSnapshotsEqual(stored, updated) {
		t.Fatal("rejected refresh changed stored invoice")
	}
	// Rich replay keeps history, even though the optimistic revision advances.
	replayed, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, updated.UpdatedAt, details)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed.Lifecycle, updated.Lifecycle) || !reflect.DeepEqual(replayed.Details, updated.Details) {
		t.Fatal("identical refresh changed lifecycle")
	}
	// Exactly one concurrent refresh can consume the captured revision.
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, replayed.UpdatedAt, &state.InvoiceDetails{PaymentTerms: "Net 60"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, state.ErrConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent refreshes succeeded=%d want=1", successes)
	}
	final, err := s.GetInvoiceByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Details.PaymentTerms != "Net 60" {
		t.Fatal("winning refresh missing")
	}
	_ = lifecycleExport(t, final)
	// Returned details/history cannot mutate the persisted snapshot.
	final.Details.PaymentTerms = "Mutated"
	for id := range final.Lifecycle.Records {
		delete(final.Lifecycle.Records, id)
	}
	check, err := s.GetInvoiceByID(ctx, inv.ID)
	if err != nil || check.Details.PaymentTerms != "Net 60" || len(check.Lifecycle.Records) == 0 {
		t.Fatal("refresh results alias stored data")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.RefreshInvoiceDetails(cancelled, inv.AccountID, inv.ID, check.UpdatedAt, details); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh error=%v", err)
	}
}

func testInvoiceRefreshHistoryLimit(t *testing.T, s state.Store, seed func(state.Invoice)) {
	t.Helper()
	ctx := context.Background()
	inv := deliverLifecycle(t, s, lifecycleFixture(t, s, "refresh-limit"))
	var sample state.InvoiceRecordLifecycle
	for _, record := range inv.Lifecycle.Records {
		sample = record
	}
	for n := len(inv.Lifecycle.Records); n < api.MaxInvoiceLifecycleRecords; n++ {
		inv.Lifecycle.Records[fmt.Sprintf("retired-%d", n)] = sample
	}
	seed(inv)
	before, err := s.GetInvoiceByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	details := &state.InvoiceDetails{PaymentTerms: "Net 60", Lines: &state.InvoiceLines{Complete: true, Items: []state.InvoiceLineItem{{ID: "new-line", ChargeCategory: "Purchase", NetCents: 1000}}}}
	if _, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, inv.UpdatedAt, details); err == nil {
		t.Fatal("refresh exceeded lifecycle history bound")
	}
	after, err := s.GetInvoiceByID(ctx, inv.ID)
	if err != nil || !refreshSnapshotsEqual(before, after) {
		t.Fatalf("failed refresh changed snapshot: %v", err)
	}
	// Existing record updates remain possible at the history cap.
	if _, err := s.RefreshInvoiceDetails(ctx, inv.AccountID, inv.ID, before.UpdatedAt, &state.InvoiceDetails{PaymentTerms: "Net 60"}); err != nil {
		t.Fatal(err)
	}
}

// PostgreSQL may decode timestamptz using time.Local, while refresh returns
// UTC. Compare the same instants without conflating their location pointers.
func refreshSnapshotsEqual(a, b state.Invoice) bool {
	normalize := func(inv state.Invoice) state.Invoice {
		inv.CreatedAt, inv.UpdatedAt = inv.CreatedAt.UTC(), inv.UpdatedAt.UTC()
		inv.PeriodStart, inv.PeriodEnd = inv.PeriodStart.UTC(), inv.PeriodEnd.UTC()
		return inv
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}
