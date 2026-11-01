//go:build !no_pg

// adr: 1027 — the storage target is journaled, fenced and lands atomically.
package managedpostgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresStorageResizeReceipt(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	d := postgresResizeDatabase(t, store, account)
	op := postgresResizeIntent(d)
	op.TargetClass, op.TargetStorageLimitBytes = d.Spec.Class, d.Spec.StorageLimitBytes*2
	if _, err := store.ReserveResize(ctx, d, op, time.Now()); err != nil {
		t.Fatal(err)
	}
	other := op
	other.TargetStorageLimitBytes++
	if _, err := store.ReserveResize(ctx, d, other, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("replay with another storage target", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_databases SET storage_limit_bytes=storage_limit_bytes*2 WHERE id=$1", d.ID); err == nil {
		t.Fatal("pending pin bypassed for storage")
	}
	now := time.Now().UTC()
	claim, err := store.Claim(ctx, account, d.ID, "worker", StateUpdating, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	stale := ObservedDatabase{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Spec: d.Spec, Status: ProviderStatusReady}
	if _, err := store.FinishResize(ctx, claim, op, stale, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("finished on the source storage limit", err)
	}
	observed := stale
	observed.Spec = op.TargetSpec()
	ready, err := store.FinishResize(ctx, claim, op, observed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetResize(ctx, account, op.ID)
	if err != nil || ready.Spec.StorageLimitBytes != op.TargetStorageLimitBytes || ready.Spec.Class != d.Spec.Class ||
		saved.TargetStorageLimitBytes != op.TargetStorageLimitBytes || saved.State != ResizeSucceeded {
		t.Fatal("receipt", ready, saved, err)
	}
	// The table refuses a shrink however it is written.
	_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_resizes(id,account_id,database_id,backend_id,backend_fingerprint,provider_resource_id,
		data_resource_id,source_spec,target_class,target_storage_limit_bytes,generation,state,created_at,completed_at)
		SELECT $1,account_id,database_id,backend_id,backend_fingerprint,provider_resource_id,data_resource_id,source_spec,target_class,1,
		generation+10,'succeeded',created_at,created_at FROM managed_postgres_resizes WHERE id=$2`, uuid.NewString(), op.ID)
	if err == nil || !strings.Contains(err.Error(), "managed_postgres_storage_resize_target_check") {
		t.Fatal("shrink row accepted", err)
	}
}
