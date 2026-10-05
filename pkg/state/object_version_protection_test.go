package state_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 582
func TestObjectVersionProtectionMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	versionProtectionSuite(t, m, nil, func(string) { now = now.Add(16 * time.Minute) })
}
func TestObjectVersionProtectionPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	versionProtectionSuite(t, s, pool, func(bucket string) {
		for _, query := range strings.Split(`UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END WHERE bucket_id=$1;UPDATE object_bucket_object_lock SET retry_at=clock_timestamp() WHERE bucket_id=$1;UPDATE object_version_protection SET retry_at=clock_timestamp(),lease_until=CASE WHEN lease_until IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END WHERE bucket_id=$1`, ";") {
			if _, err := pool.Exec(ctx, query, bucket); err != nil {
				t.Fatal(err)
			}
		}
	})
}
func versionProtectionSuite(t *testing.T, st accountingStore, pool *pgxpool.Pool, advance func(string)) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	lock := st.(state.ObjectBucketObjectLockStore)
	cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true}
	if _, err := lock.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, cfg); err != nil {
		t.Fatal(err)
	}
	finishLockVersioning(t, st, b, advance)
	j, err := lock.ClaimObjectBucketObjectLock(ctx, b.ID, "prepare-lock")
	if err != nil {
		t.Fatal(j, err)
	}
	if _, err = lock.FinishObjectBucketObjectLock(ctx, b.ID, j.Token, cfg); err != nil {
		t.Fatal(err)
	}
	refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(ctx, b.AccountID, b.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "private-exact-version"}})
	if err != nil {
		t.Fatal(err)
	}
	ops := st.(state.ObjectVersionProtectionStore)
	input := state.ObjectVersionProtection{ObjectVersionProtection: api.ObjectVersionProtection{ID: uuid.NewString(), BucketID: b.ID, Key: "key", VersionID: refs[0].ID, Kind: "legal_hold", LegalHold: &api.ObjectVersionLegalHold{Status: "ON"}}, AccountID: b.AccountID, AppID: b.AppID}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := ops.BeginObjectVersionProtection(ctx, input); errorsCh <- e }()
	}
	wg.Wait()
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal("same intent race", e)
		}
	}
	saved, err := ops.GetObjectVersionProtection(ctx, b.AccountID, b.ID, input.ID)
	if err != nil || saved.ProviderVersionID != "private-exact-version" {
		t.Fatal(saved, err)
	}
	input.LegalHold.Status = "OFF"
	if _, err = ops.BeginObjectVersionProtection(ctx, input); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed retry intent", err)
	}
	input.LegalHold.Status = "ON"
	if _, err = ops.GetObjectVersionProtection(ctx, uuid.NewString(), b.ID, input.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign read", err)
	}
	foreign := input
	foreign.ID = uuid.NewString()
	foreign.Key = "other"
	if _, err = ops.BeginObjectVersionProtection(ctx, foreign); !errors.Is(err, state.ErrConflict) {
		t.Fatal("competing intent", err)
	}
	if _, err = st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "cleanup", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cleanup bypassed protection", err)
	}
	write := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: uuid.NewString(), Key: "other", Bytes: 1, Status: "pending"}
	if _, err = st.(state.ObjectTrackedGatewayUploadStore).BeginTrackedGatewayUpload(ctx, write, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("write bypassed protection", err)
	}
	if _, _, err = st.(state.ObjectDeletionStore).BeginObjectDeletion(ctx, state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "key", Selector: refs[0].ID}, AccountID: b.AccountID, AppID: b.AppID, Token: "delete"}, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("delete bypassed protection", err)
	}
	jop, err := ops.ClaimObjectVersionProtection(ctx, input.ID, "worker-one")
	if err != nil {
		t.Fatal(jop, err)
	}
	if _, err = ops.ClaimObjectVersionProtection(ctx, input.ID, "worker-two"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("concurrent workers", err)
	}
	jop, err = ops.DispatchObjectVersionProtection(ctx, input.ID, jop.Token)
	if err != nil || !jop.Dispatched {
		t.Fatal(jop, err)
	}
	if _, err = ops.DispatchObjectVersionProtection(ctx, input.ID, jop.Token); !errors.Is(err, state.ErrConflict) {
		t.Fatal("second dispatch", err)
	}
	if pool != nil {
		for _, query := range []string{`UPDATE object_version_protection SET native_version_id='wrong' WHERE id=$1`, `DELETE FROM object_version_protection WHERE id=$1`, `UPDATE object_version_protection SET dispatched=false WHERE id=$1`, `UPDATE object_buckets SET state='deleting',lease_token='raw',lease_until=now()+interval '1 minute' WHERE id=$1`} {
			id := input.ID
			if strings.Contains(query, "UPDATE object_buckets") {
				id = b.ID
			}
			if _, e := pool.Exec(ctx, query, id); e == nil {
				t.Fatal("raw writer bypass", query)
			}
		}
	}
	advance(b.ID)
	if pool != nil {
		ops = state.NewPgStore(pool)
	}
	jop, err = ops.ClaimObjectVersionProtection(ctx, input.ID, "restarted")
	if err != nil || !jop.Dispatched {
		t.Fatal(jop, err)
	}
	if _, err = ops.DispatchObjectVersionProtection(ctx, input.ID, "restarted"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("restart repeated mutation", err)
	}
	if _, err = ops.FinishObjectVersionProtection(ctx, input.ID, "worker-one", "ready", ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale lease settled", err)
	}
	if _, err = ops.FinishObjectVersionProtection(ctx, input.ID, "restarted", "failed", "preparation_failed"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("uncertain dispatch discarded", err)
	}
	jop, err = ops.FinishObjectVersionProtection(ctx, input.ID, "restarted", "ready", "")
	if err != nil || jop.State != "ready" {
		t.Fatal(jop, err)
	}
	// Legacy memory admission uses the wall clock; restore it before fresh inventory.
	if m, ok := st.(*state.MemStore); ok {
		m.SetClockForTest(time.Now)
	}
	cap := st.(state.ObjectCapacityStore)
	fresh, e := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	fresh, e = cap.ClaimObjectCapacityReconciliation(ctx, fresh.ID, "refresh")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, fresh.ID, fresh.Token, "", nil); e != nil {
		t.Fatal(e)
	}
	if _, err = st.(state.ObjectTrackedGatewayUploadStore).BeginTrackedGatewayUpload(ctx, write, accountingPolicy()); err != nil {
		t.Fatal("settlement did not release fence", err)
	}
	input.LegalHold.Status = "ON"
	replay, e := ops.BeginObjectVersionProtection(ctx, input)
	if e != nil || replay.State != "ready" {
		t.Fatal("terminal replay", replay, e)
	}
}
