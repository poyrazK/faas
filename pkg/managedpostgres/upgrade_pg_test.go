//go:build !no_pg

// adr: 1027 — a major upgrade is journaled and fenced like a resize, lands
// the major atomically, and a rejected one ends failed for good with the
// database back on its source spec.
package managedpostgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func postgresUpgradeIntent(d Database, major int) ResizeOperation {
	op := postgresResizeIntent(d)
	op.TargetClass, op.TargetPostgresMajor = d.Spec.Class, major
	return op
}

func TestPostgresMajorUpgradeReceipt(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	d := postgresResizeDatabase(t, store, account)
	op := postgresUpgradeIntent(d, d.Spec.PostgresMajor+1)
	if _, err := store.ReserveResize(ctx, d, op, time.Now()); err != nil {
		t.Fatal(err)
	}
	other := op
	other.TargetPostgresMajor++
	if _, err := store.ReserveResize(ctx, d, other, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("replay with another major", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_databases SET postgres_major=postgres_major+1 WHERE id=$1", d.ID); err == nil {
		t.Fatal("pending pin bypassed for the major")
	}
	now := time.Now().UTC()
	claim, err := store.Claim(ctx, account, d.ID, "worker", StateUpdating, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	observed := ObservedDatabase{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Spec: op.TargetSpec(), Status: ProviderStatusReady}
	ready, err := store.FinishResize(ctx, claim, op, observed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetResize(ctx, account, op.ID)
	if err != nil || ready.Spec.PostgresMajor != op.TargetPostgresMajor || ready.Spec.Class != d.Spec.Class ||
		saved.TargetPostgresMajor != op.TargetPostgresMajor || saved.State != ResizeSucceeded || !saved.MajorUpgrade() {
		t.Fatal("receipt", ready, saved, err)
	}
	// The table refuses an upgrade that changes anything else, or none.
	for name, row := range map[string]string{
		"with a class change": `'burstable',$3::int`,
		"to the same major":   `target_class,(source_spec->>'PostgresMajor')::int+0*$3::int`,
	} {
		_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_resizes(id,account_id,database_id,backend_id,backend_fingerprint,provider_resource_id,
			data_resource_id,source_spec,target_class,target_postgres_major,generation,state,created_at,completed_at)
			SELECT $1,account_id,database_id,backend_id,backend_fingerprint,provider_resource_id,data_resource_id,source_spec,`+row+`,
			generation+10,'succeeded',created_at,created_at FROM managed_postgres_resizes WHERE id=$2`, uuid.NewString(), op.ID, op.TargetPostgresMajor)
		if err == nil || !strings.Contains(err.Error(), "managed_postgres_major_upgrade_target_check") {
			t.Errorf("upgrade %s accepted: %v", name, err)
		}
	}
}

func TestPostgresMajorUpgradeFailsForGood(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	d := postgresResizeDatabase(t, store, account)
	op := postgresUpgradeIntent(d, d.Spec.PostgresMajor+1)
	if _, err := store.ReserveResize(ctx, d, op, time.Now()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim, err := store.Claim(ctx, account, d.ID, "worker", StateUpdating, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailResize(ctx, claim, op, "Not A Code", time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatal("an invalid code accepted", err)
	}
	stale := claim
	stale.LeaseToken = "another-worker"
	if _, err := store.FailResize(ctx, stale, op, "upgrade_refused", time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("failed under another lease", err)
	}
	back, err := store.FailResize(ctx, claim, op, "upgrade_refused", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetResize(ctx, account, op.ID)
	if err != nil || back.State != StateReady || back.Spec != d.Spec || back.ObservedGeneration != op.Generation || back.DesiredGeneration != op.Generation ||
		saved.State != ResizeFailed || saved.ErrorCode != "upgrade_refused" || saved.CompletedAt.IsZero() {
		t.Fatal("failure", back, saved, err)
	}
	if _, err := store.ActiveResize(ctx, account, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("a failed upgrade is still active", err)
	}
	// The outcome is final, and only an upgrade can fail.
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_resizes SET state='succeeded',error_code=NULL WHERE id=$1", op.ID); err == nil {
		t.Fatal("a failed upgrade was rewritten")
	}
	_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_resizes(id,account_id,database_id,backend_id,backend_fingerprint,provider_resource_id,
		data_resource_id,source_spec,target_class,generation,state,error_code,created_at,completed_at)
		SELECT $1,account_id,database_id,backend_id,backend_fingerprint,provider_resource_id,data_resource_id,source_spec,'burstable',
		generation+10,'failed','resize_refused',created_at,created_at FROM managed_postgres_resizes WHERE id=$2`, uuid.NewString(), op.ID)
	if err == nil || !strings.Contains(err.Error(), "managed_postgres_resize_outcome_check") {
		t.Fatal("a failed resize accepted", err)
	}
	// A new upgrade may follow.
	next := postgresUpgradeIntent(back, back.Spec.PostgresMajor+1)
	if _, err := store.ReserveResize(ctx, back, next, time.Now()); err != nil {
		t.Fatal("upgrade after a failed one", err)
	}
}
