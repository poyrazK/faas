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
