//go:build !no_pg

package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func productBillingPGFixture(t *testing.T, s *PgStore) productBillingFixture {
	t.Helper()
	insert := func(op Operation) {
		t.Helper()
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if err := insertProductOperationTx(t.Context(), tx, op); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	settle := func(op Operation) {
		t.Helper()
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if err := settleProductOperationTx(t.Context(), tx, op); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return seedProductBilling(t, s, insert, settle)
}

func TestProductControlBillingPostgres(t *testing.T) {
	s := newPgStore(t)
	productBillingContract(t, s, productBillingPGFixture(t, s))
}

func TestProductControlBillingFinalizationWaitsForSettlement(t *testing.T) {
	s := newPgStore(t)
	f := productBillingPGFixture(t, s)
	ctx := t.Context()
	if _, err := s.WaiveProductReceipt(ctx, f.scope, f.ops["expired"].ID, "pilot write-off"); err != nil {
		t.Fatal(err)
	}
	review, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op := f.ops["unknown"]
	op.State = api.OperationSucceeded
	op.UpdatedAt = time.Now().UTC()
	if err := settleProductOperationTx(ctx, tx, op); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.FinalizeProductBilling(ctx, f.scope, review.ReviewHash); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("finalization passed an uncommitted outcome: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrProductBillingReviewChanged) {
			t.Fatalf("stale review accepted: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("billing/settlement lock deadlock")
	}
	updated, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil || updated.Unresolved != 0 || updated.AmountMillicents != 60000 || updated.Finalized != nil {
		t.Fatalf("settled review: %+v %v", updated, err)
	}
	if _, err := s.FinalizeProductBilling(ctx, f.scope, updated.ReviewHash); err != nil {
		t.Fatal(err)
	}
}

// adr: 20261020090607 — purging an app keeps its Product Control receipts,
// waivers, statements, invoice handoffs and catalog revisions, and drops its
// configuration; deleting the account erases the billing records too.
func TestProductControlBillingSurvivesAppPurgePostgres(t *testing.T) {
	s := newPgStore(t)
	f := productBillingPGFixture(t, s)
	ctx := t.Context()
	review, err := s.ReviewProductBilling(ctx, f.other)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := s.FinalizeProductBilling(ctx, f.other, review.ReviewHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandoffProductBilling(ctx, f.other, statement.ID, "INV-retained"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WaiveProductReceipt(ctx, f.scope, f.ops["unknown"].ID, "lost exporter outcome"); err != nil {
		t.Fatal(err)
	}
	counts := func() map[string]int {
		t.Helper()
		out := map[string]int{}
		for name, query := range map[string]string{
			"receipts":  `select count(*) from product_control_receipts where app_id = $1`,
			"waivers":   `select count(*) from product_control_waivers w join product_control_receipts r using (operation_id) where r.app_id = $1`,
			"documents": `select count(*) from product_control_billing_documents where app_id = $1`,
			"handoffs":  `select count(*) from product_control_invoice_handoffs h join product_control_billing_documents d on d.id = h.document_id where d.app_id = $1`,
			"revisions": `select count(*) from product_control_revisions where app_id = $1`,
			"heads":     `select count(*) from product_control_heads where app_id = $1`,
			"customers": `select count(*) from product_control_customers where app_id = $1`,
		} {
			var n int
			if err := s.pool.QueryRow(ctx, query, f.scope.AppID).Scan(&n); err != nil {
				t.Fatalf("count %s: %v", name, err)
			}
			out[name] = n
		}
		return out
	}
	before := counts()
	if before["receipts"] != 8 || before["waivers"] != 1 || before["documents"] != 1 || before["handoffs"] != 1 || before["revisions"] == 0 ||
		before["heads"] != 1 || before["customers"] != 3 {
		t.Fatalf("seeded rows = %v", before)
	}

	if _, err := s.ScheduleAppDeletion(ctx, f.scope.AppID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(ctx, f.scope.AppID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(ctx, f.scope.AppID); err != nil {
		t.Fatalf("purge an app with Product Control billing: %v", err)
	}
	after := counts()
	for _, kept := range []string{"receipts", "waivers", "documents", "handoffs", "revisions"} {
		if after[kept] != before[kept] {
			t.Fatalf("app purge erased %s: %v, before %v", kept, after, before)
		}
	}
	if after["heads"] != 0 || after["customers"] != 0 {
		t.Fatalf("app purge kept configuration: %v", after)
	}
	if _, err := s.ReviewProductBilling(ctx, f.other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a purged app's billing is still served: %v", err)
	}

	if err := s.MarkAccountDeletionPending(ctx, f.scope.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, f.scope.AccountID); err != nil {
		t.Fatal(err)
	}
	for name, n := range counts() {
		if n != 0 {
			t.Fatalf("account deletion kept %d %s", n, name)
		}
	}
}
