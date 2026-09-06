package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type recoveryStore interface {
	state.Store
	state.ObjectBucketStore
}

func seedRecoveryBucket(t *testing.T, st recoveryStore) state.ObjectBucket {
	t.Helper()
	ctx := context.Background()
	a, err := st.CreateAccount(ctx, "recovery-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "recover-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: a.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: "external", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + uuid.NewString()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestObjectBucketRecoveryMem(t *testing.T) { recoveryStoreSuite(t, state.NewMemStore()) }
func TestObjectBucketRecoveryPG(t *testing.T)  { st, _ := pgStore(t); recoveryStoreSuite(t, st) }

func recoveryStoreSuite(t *testing.T, st recoveryStore) {
	t.Helper()
	ctx := context.Background()
	b := seedRecoveryBucket(t, st)
	if rows, err := st.DueObjectBuckets(ctx, false, 20); err != nil || len(rows) != 0 {
		t.Fatal("disabled provisioning", rows, err)
	}
	if rows, err := st.DueObjectBuckets(ctx, true, 20); err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	// Only one replica may obtain the current operation's lease.
	winners := make(chan state.ObjectBucket, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, err := st.ClaimObjectBucketRecovery(ctx, b.AccountID, b.AppID, b.ID, uuid.NewString(), "provisioning")
			if err == nil {
				winners <- row
			} else if !errors.Is(err, state.ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatal("lease winners", len(winners))
	}
	claimed := <-winners
	if claimed.AttemptCount != 1 {
		t.Fatal(claimed)
	}
	if rows, err := st.DueObjectBuckets(ctx, true, 20); err != nil || len(rows) != 0 {
		t.Fatal("active lease offered", rows, err)
	}
	if err := st.RetryObjectBucket(ctx, b.ID, "wrong", "temporary", time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unowned retry", err)
	}
	if err := st.RetryObjectBucket(ctx, b.ID, claimed.LeaseToken, "raw provider secret", time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unbounded error persisted", err)
	}
	if err := st.RetryObjectBucket(ctx, b.ID, claimed.LeaseToken, "temporary", time.Minute); err != nil {
		t.Fatal(err)
	}
	if rows, err := st.DueObjectBuckets(ctx, true, 20); err != nil || len(rows) != 0 {
		t.Fatal("cooldown ignored", rows, err)
	}
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "request-retry", "provisioning"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("request bypassed cooldown", err)
	}
	// Cleanup may change a failed provisioning intent without waiting for its retry.
	claimed, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "cleanup", "deleting")
	if err != nil || claimed.AttemptCount != 1 || claimed.RetryAt.After(time.Now()) || claimed.LastErrorCode != "" {
		t.Fatal(claimed, err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "cleanup", "ready"); err != nil {
		t.Fatal(err)
	}
	// A stale deletion candidate must NOT re-delete a now-ready bucket.
	if _, err := st.ClaimObjectBucketRecovery(ctx, b.AccountID, b.AppID, b.ID, "stale-worker", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale recovery changed intent", err)
	}
	got, err := st.GetObjectBucket(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || got.AttemptCount != 0 || got.LastErrorCode != "" {
		t.Fatal(got, err)
	}
}

func TestObjectBucketRecoveryExpiredLeasePG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b := seedRecoveryBucket(t, st)
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "crashed", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_buckets SET lease_until=now()-interval '1 second' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := st.DueObjectBuckets(ctx, true, 20)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	recovered, err := st.ClaimObjectBucketRecovery(ctx, b.AccountID, b.AppID, b.ID, "recovered", "provisioning")
	if err != nil || recovered.AttemptCount != 2 || recovered.PhysicalName != b.PhysicalName {
		t.Fatal(recovered, err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "crashed", "ready"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale completion accepted", err)
	}
	if err := st.RetryObjectBucket(ctx, b.ID, "crashed", "temporary", time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale retry accepted", err)
	}
	if err := st.RetryObjectBucket(ctx, b.ID, "recovered", "configuration", time.Hour); err != nil {
		t.Fatal(err)
	}
	if rows, err := st.DueObjectBuckets(ctx, true, 20); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_buckets SET retry_at=now()-interval '1 second' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err = st.ClaimObjectBucketRecovery(ctx, b.AccountID, b.AppID, b.ID, "third", "provisioning")
	if err != nil || recovered.AttemptCount != 3 {
		t.Fatal(recovered, err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "third", "ready"); err != nil {
		t.Fatal(err)
	}
}
