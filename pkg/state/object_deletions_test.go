package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 547
func TestObjectDeletionRecoveryOrderMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
	for _, id := range ids {
		b, _ := seedAccounting(t, m)
		j, created, e := m.BeginObjectDeletion(t.Context(), state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: id, BucketID: b.ID, Key: "key"}, AccountID: b.AccountID, AppID: b.AppID, Token: "request"}, accountingPolicy())
		if e != nil || !created {
			t.Fatal(j, created, e)
		}
		if _, e = m.DispatchObjectDeletion(t.Context(), id, j.Token, "", nil); e != nil {
			t.Fatal(e)
		}
	}
	now = now.Add(api.ObjectDeletionLease + time.Second)
	j, e := m.ClaimObjectDeletion(t.Context(), ids[0], "configuration")
	if e != nil {
		t.Fatal(j, e)
	}
	if e = m.RetryObjectDeletion(t.Context(), j.ID, j.Token, "configuration"); e != nil {
		t.Fatal(e)
	}
	// Even when both rows are due again, the untouched row has priority.
	now = now.Add(api.ObjectDeletionRetry + time.Second)
	rows, e := m.DueObjectDeletions(t.Context(), 1)
	if e != nil || len(rows) != 1 || rows[0].ID != ids[1] {
		t.Fatal("deferred low UUID monopolized recovery", rows, e)
	}
}

// adr: 547
func TestObjectDeletionMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	objectDeletionSuite(t, m, func() { now = now.Add(api.ObjectDeletionLease + api.ObjectDeletionRetry + time.Second) })
}
func TestObjectDeletionPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	objectDeletionSuite(t, st, func() {
		if _, e := pool.Exec(ctx, `UPDATE object_deletions SET lease_until=CASE WHEN lease_token='' THEN NULL ELSE now()-interval '1 second' END,retry_at=now() WHERE state IN ('prepared','dispatched')`); e != nil {
			t.Fatal(e)
		}
	})
}
func objectDeletionSuite(t *testing.T, st accountingStore, advance func()) {
	b, _ := seedAccounting(t, st)
	d := st.(state.ObjectDeletionStore)
	cap := st.(state.ObjectCapacityStore)
	v := st.(state.ObjectBucketVersioningStore)
	ctx := t.Context()
	input := state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "key"}, AccountID: b.AccountID, AppID: b.AppID, Token: "request"}
	j, created, e := d.BeginObjectDeletion(ctx, input, accountingPolicy())
	if e != nil || !created || j.State != "prepared" {
		t.Fatal(j, created, e)
	}
	input.Token = "retry"
	replay, new, e := d.BeginObjectDeletion(ctx, input, accountingPolicy())
	if e != nil || new || replay.Token != "request" {
		t.Fatal(replay, new, e)
	}
	input.Key = "other"
	if _, _, e = d.BeginObjectDeletion(ctx, input, accountingPolicy()); !errors.Is(e, state.ErrConflict) {
		t.Fatal("changed payload", e)
	}
	input.AccountID = uuid.NewString()
	if _, _, e = d.BeginObjectDeletion(ctx, input, accountingPolicy()); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("foreign receipt", e)
	}
	if _, e = d.GetObjectDeletion(ctx, uuid.NewString(), b.ID, j.ID); !errors.Is(e, state.ErrNotFound) {
		t.Fatal(e)
	}
	if e = cap.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "new", 1, accountingPolicy()); !errors.Is(e, state.ErrConflict) {
		t.Fatal("write passed fence", e)
	}
	if _, e = v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled"); !errors.Is(e, state.ErrConflict) {
		t.Fatal("versioning passed fence", e)
	}
	if _, e = cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(e, state.ErrConflict) {
		t.Fatal("inventory passed fence", e)
	}
	if _, e = st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "bucket-delete", "deleting"); !errors.Is(e, state.ErrConflict) {
		t.Fatal("bucket deletion passed fence", e)
	}
	if _, e = d.DispatchObjectDeletion(ctx, j.ID, "stale", "", nil); !errors.Is(e, state.ErrConflict) {
		t.Fatal("stale dispatch", e)
	}
	j, e = d.DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil)
	if e != nil || j.State != "dispatched" {
		t.Fatal(j, e)
	}
	if _, e = d.DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil); !errors.Is(e, state.ErrConflict) {
		t.Fatal("second dispatch", e)
	}
	failed := j
	failed.State = "failed"
	failed.LastErrorCode = "preparation_expired"
	if _, e = d.FinishObjectDeletion(ctx, failed); !errors.Is(e, state.ErrConflict) {
		t.Fatal("timer settled dispatched mutation", e)
	}
	if e = d.RetryObjectDeletion(ctx, j.ID, j.Token, "provider_uncertain"); e != nil {
		t.Fatal(e)
	}
	advance()
	j, e = d.ClaimObjectDeletion(ctx, j.ID, "recover")
	if e != nil {
		t.Fatal(j, e)
	}
	failed = j
	failed.State = "failed"
	failed.LastErrorCode = "provider_rejected"
	if _, e = d.FinishObjectDeletion(ctx, failed); !errors.Is(e, state.ErrConflict) {
		t.Fatal("recovery rejection erased earlier uncertain dispatch", e)
	}
	if e = cap.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "new", 1, accountingPolicy()); !errors.Is(e, state.ErrConflict) {
		t.Fatal("expiry released fence", e)
	}
	j.State = "completed"
	j.LastErrorCode = ""
	j, e = d.FinishObjectDeletion(ctx, j)
	if e != nil {
		t.Fatal(j, e)
	}
	if e = cap.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "new", 1, accountingPolicy()); e != nil {
		t.Fatal("completed intent retained fence", e)
	}
	// A prepared intent can be cancelled after lease expiry, without dispatch.
	other, _ := seedAccounting(t, st)
	input = state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: other.ID, Key: "key"}, AccountID: other.AccountID, AppID: other.AppID, Token: "prepare"}
	j, _, e = d.BeginObjectDeletion(ctx, input, accountingPolicy())
	if e != nil {
		t.Fatal(e)
	}
	advance()
	j, e = d.ClaimObjectDeletion(ctx, j.ID, "cancel")
	if e != nil {
		t.Fatal(e)
	}
	j.State = "failed"
	j.LastErrorCode = "preparation_expired"
	if _, e = d.FinishObjectDeletion(ctx, j); e != nil {
		t.Fatal(e)
	}
	if _, e = d.DispatchObjectDeletion(ctx, j.ID, "cancel", "", []string{strings.Repeat("a", 64)}); !errors.Is(e, state.ErrConflict) {
		t.Fatal("cancelled intent dispatched", e)
	}
}

func TestObjectDeletionVersioningRaceMem(t *testing.T) {
	objectDeletionVersioningRace(t, state.NewMemStore())
}
func TestObjectDeletionVersioningRacePG(t *testing.T) {
	st, _ := pgStore(t)
	objectDeletionVersioningRace(t, st)
}

func TestObjectDeletionLegacyCleanupMem(t *testing.T) {
	objectDeletionLegacyCleanup(t, state.NewMemStore())
}
func TestObjectDeletionLegacyCleanupPG(t *testing.T) {
	st, _ := pgStore(t)
	objectDeletionLegacyCleanup(t, st)
}
func objectDeletionLegacyCleanup(t *testing.T, st accountingStore) {
	t.Helper()
	b, _ := seedAccounting(t, st)
	ctx := t.Context()
	if e := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "legacy", 10, true, accountingPolicy()); e != nil {
		t.Fatal(e)
	}
	d := st.(state.ObjectDeletionStore)
	j, _, e := d.BeginObjectDeletion(ctx, state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "legacy"}, AccountID: b.AccountID, AppID: b.AppID, Token: "cleanup"}, accountingPolicy())
	if e != nil {
		t.Fatal("legacy unversioned cleanup blocked", e)
	}
	j, e = d.DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	j.State = "completed"
	if _, e = d.FinishObjectDeletion(ctx, j); e != nil {
		t.Fatal(e)
	}
	u, e := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if e != nil || u.Buckets[0].GrantedBytes != 10 || u.Buckets[0].GrantedKeys != 1 {
		t.Fatal("cleanup refunded unproven legacy grants", u, e)
	}
}
func objectDeletionVersioningRace(t *testing.T, st accountingStore) {
	t.Helper()
	b, _ := seedAccounting(t, st)
	d := st.(state.ObjectDeletionStore)
	v := st.(state.ObjectBucketVersioningStore)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, _, e := d.BeginObjectDeletion(t.Context(), state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "key"}, AccountID: b.AccountID, AppID: b.AppID, Token: "delete"}, accountingPolicy())
		results <- e
	}()
	go func() {
		<-start
		_, e := v.RequestObjectBucketVersioning(t.Context(), b.AccountID, b.AppID, b.ID, "Enabled")
		results <- e
	}()
	close(start)
	wins := 0
	for range 2 {
		e := <-results
		if e == nil {
			wins++
		} else if !errors.Is(e, state.ErrConflict) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatal("delete and versioning admitted together", wins)
	}
}
