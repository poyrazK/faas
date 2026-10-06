//go:build !no_pg

// adr: 624 — immutable policy history and full target receipt guard.
package managedpostgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
)

func TestPostgresComputePolicyReceiptFencesOldReconciler(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	database := postgresResizeDatabase(t, store, account)
	operation := postgresResizeIntent(database)
	operation.PolicyChange, operation.TargetScaleToZero, operation.TargetClass = true, !database.Spec.ScaleToZero, database.Spec.Class
	if _, err := store.ReserveResize(ctx, database, operation, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE managed_postgres_resizes SET target_scale_to_zero=NOT target_scale_to_zero WHERE id=$1",
		"DELETE FROM managed_postgres_resizes WHERE id=$1",
	} {
		if _, err := pool.Exec(ctx, query, operation.ID); err == nil {
			t.Fatal("intent changed", query)
		}
	}
	now := time.Now().UTC()
	claimed, err := store.Claim(ctx, account, database.ID, "policy-worker", StateUpdating, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// An old binary would complete the generation and class without applying the
	// new policy. The deferred receipt rejects its whole transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "UPDATE managed_postgres_resizes SET state='succeeded',completed_at=clock_timestamp() WHERE id=$1", operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE managed_postgres_databases SET state='ready',observed_generation=desired_generation WHERE id=$1", database.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("old reconciler published unchanged policy")
	}
	forged := operation
	forged.PolicyChange = false
	observed := ObservedDatabase{ProviderResourceID: database.ProviderResourceID, DataResourceID: database.DataResourceID, Spec: operation.TargetSpec(), Status: ProviderStatusReady}
	if _, err := store.FinishResize(ctx, claimed, forged, observed, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("forged kind", err)
	}
	ready, err := store.FinishResize(ctx, claimed, operation, observed, time.Now())
	if err != nil || ready.Spec != operation.TargetSpec() {
		t.Fatal("atomic completion", ready, err)
	}
	raw, err := migrations.FS.ReadFile("20261006133120641_managed_postgres_compute_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err := pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback erased completed policy history")
	}
	history, err := store.GetResize(ctx, account, operation.ID)
	if err != nil || !history.PolicyChange || history.TargetScaleToZero != operation.TargetScaleToZero || history.State != ResizeSucceeded {
		t.Fatal("policy history lost", history, err)
	}
}

func TestPostgresComputePolicyMigrationPreservesPendingClassIntent(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	raw, err := migrations.FS.ReadFile("20261006133120641_managed_postgres_compute_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	database := postgresResizeDatabase(t, store, account)
	operation := postgresResizeIntent(database)
	if _, err := store.ReserveResize(ctx, database, operation, time.Now()); err != nil {
		t.Fatal(err)
	}
	// A class-only pending intent retains its old schema-compatible contract.
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("down with class intent", err)
	}
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("up with class intent", err)
	}
	history, err := store.GetResize(ctx, account, operation.ID)
	if err != nil || history.PolicyChange || history.State != ResizePending {
		t.Fatal(history, err)
	}
}
