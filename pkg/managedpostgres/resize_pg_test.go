//go:build !no_pg

// adr: 623 — atomic intent, receipt and server-clock lease fences.
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
)

func postgresResizeDatabase(t *testing.T, store *PostgresStore, account string) Database {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	d, _, err := store.Reserve(ctx, postgresTestDatabase(account, "resize-"+uuid.NewString()[:8], now), 100)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, account, d.ID, uuid.NewString(), StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordProviderResource(ctx, d.ID, claimed.LeaseToken, "project-"+d.ID, now); err != nil {
		t.Fatal(err)
	}
	claimed.ProviderResourceID = "project-" + d.ID
	observed := ObservedDatabase{ProviderResourceID: claimed.ProviderResourceID, DataResourceID: claimed.ProviderResourceID + "/branch", Spec: d.Spec, Status: ProviderStatusReady}
	d, err = store.FinishProvisionWithDataResource(ctx, claimed, observed, now)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func postgresResizeIntent(d Database) ResizeOperation {
	return ResizeOperation{ID: uuid.NewString(), AccountID: d.AccountID, DatabaseID: d.ID, BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint,
		ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, SourceSpec: d.Spec, TargetClass: ClassBurstable, Generation: d.DesiredGeneration + 1, State: ResizePending, CreatedAt: time.Now().UTC()}
}
func TestPostgresResizeAtomicReceiptAndDatabasePins(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	d := postgresResizeDatabase(t, store, account)
	op := postgresResizeIntent(d)
	reserved, err := store.ReserveResize(ctx, d, op, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.ReserveResize(ctx, d, op, time.Now())
	if err != nil || replay.ID != reserved.ID {
		t.Fatal("replay", err)
	}
	for _, query := range []string{
		"UPDATE managed_postgres_databases SET service_class='burstable' WHERE id=$1",
		"UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1",
		"UPDATE managed_postgres_databases SET data_resource_id='replacement' WHERE id=$1",
		"DELETE FROM managed_postgres_databases WHERE id=$1",
	} {
		if _, err := pool.Exec(ctx, query, d.ID); err == nil {
			t.Fatal("pending pin bypassed", query)
		}
	}
	if _, err := pool.Exec(ctx, "DELETE FROM managed_postgres_resizes WHERE id=$1", op.ID); err == nil {
		t.Fatal("pending intent removed")
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_resizes SET target_class='production' WHERE id=$1", op.ID); err == nil {
		t.Fatal("intent mutated")
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_resizes SET state='succeeded',completed_at=clock_timestamp() WHERE id=$1", op.ID); err == nil {
		t.Fatal("receipt completed without observed generation")
	}
	other := postgresResizeIntent(d)
	if _, err := store.ReserveResize(ctx, d, other, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("second intent", err)
	}
	now := time.Now().UTC()
	claim, err := store.Claim(ctx, account, d.ID, "worker", StateUpdating, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	observed := ObservedDatabase{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Spec: op.TargetSpec(), Status: ProviderStatusReady}
	drift := observed
	drift.DataResourceID = "replacement"
	if _, err := store.FinishResize(ctx, claim, op, drift, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("data drift accepted", err)
	}
	forged := op
	forged.DataResourceID = "replacement"
	if _, err := store.FinishResize(ctx, claim, forged, drift, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("forged snapshot bypassed durable data pin", err)
	}
	ready, err := store.FinishResize(ctx, claim, op, observed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetResize(ctx, account, op.ID)
	if err != nil || saved.State != ResizeSucceeded || saved.CompletedAt.IsZero() || ready.State != StateReady || ready.ObservedGeneration != 2 || ready.Spec != op.TargetSpec() || ready.DataResourceID != d.DataResourceID {
		t.Fatal("atomic result", ready, saved, err)
	}
	op2 := postgresResizeIntent(ready)
	op2.TargetClass = ClassDevelopment
	if _, err := store.ReserveResize(ctx, ready, op2, time.Now()); err != nil {
		t.Fatal("next generation", err)
	}
	if _, err := store.FinishResize(ctx, claim, op, observed, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("old generation completed", err)
	}
	if _, err := store.GetResize(ctx, uuid.NewString(), op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross account", err)
	}
}
func TestPostgresResizeLeaseExpiryAfterDatabaseLockWait(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	d := postgresResizeDatabase(t, store, account)
	op := postgresResizeIntent(d)
	if _, err := store.ReserveResize(ctx, d, op, time.Now()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim, err := store.Claim(ctx, account, d.ID, "expiring", StateUpdating, now, now.Add(300*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM managed_postgres_databases WHERE id=$1 FOR UPDATE", d.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.FinishResize(ctx, claim, op, ObservedDatabase{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Spec: op.TargetSpec(), Status: ProviderStatusReady}, now)
		done <- err
	}()
	time.Sleep(time.Until(claim.LeaseUntil) + 30*time.Millisecond)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrConflict) {
		t.Fatal("client timestamp bypassed expiry", err)
	}
	saved, _ := store.GetResize(ctx, account, op.ID)
	if saved.State != ResizePending {
		t.Fatal("partial receipt committed")
	}
	replacementNow := time.Now().UTC()
	replacement, err := store.Claim(ctx, account, d.ID, "replacement", StateUpdating, replacementNow, replacementNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if replacement.LeaseToken == claim.LeaseToken {
		t.Fatal("replacement lease not issued")
	}
}
func TestPostgresResizeAndDeleteReservationSerialized(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	for i := 0; i < 5; i++ {
		d := postgresResizeDatabase(t, store, account)
		op := postgresResizeIntent(d)
		var rerr, derr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, rerr = store.ReserveResize(ctx, d, op, time.Now()) }()
		go func() {
			defer wg.Done()
			now := time.Now()
			_, derr = store.ClaimDelete(ctx, account, d.ID, "delete", now, now.Add(time.Minute))
		}()
		wg.Wait()
		if (rerr == nil) == (derr == nil) {
			t.Fatalf("both accepted/rejected: %v %v", rerr, derr)
		}
	}
}
func TestPostgresResizeRejectsChangedBackendSnapshot(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	d := postgresResizeDatabase(t, store, account)
	op := postgresResizeIntent(d)
	op.BackendFingerprint = strings.Repeat("b", 64)
	if _, err := store.ReserveResize(ctx, d, op, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatal("unpinned backend", err)
	}
}

func TestPostgresResizeMigrationReplayAndRollbackFence(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	raw, err := migrations.FS.ReadFile("20261005174532362_managed_postgres_resize.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("replay up", err)
	}
	d := postgresResizeDatabase(t, store, account)
	op := postgresResizeIntent(d)
	if _, err := store.ReserveResize(ctx, d, op, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback erased unresolved intent")
	}
	now := time.Now()
	claimed, err := store.Claim(ctx, account, d.ID, "finish", StateUpdating, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	observed := ObservedDatabase{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Spec: op.TargetSpec(), Status: ProviderStatusReady}
	if _, err := store.FinishResize(ctx, claimed, op, observed, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("completed rollback", err)
	}
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("up after down", err)
	}
}
