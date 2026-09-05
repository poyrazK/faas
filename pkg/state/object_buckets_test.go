package state_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectBucketStoreMem(t *testing.T) { objectBucketStoreSuite(t, state.NewMemStore()) }
func TestObjectBucketStorePG(t *testing.T)  { st, _ := pgStore(t); objectBucketStoreSuite(t, st) }

func objectBucketStoreSuite(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	st := base.(state.ObjectBucketStore)
	acct, err := base.CreateAccount(ctx, "buckets@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := base.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "bucket-store", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	makeBucket := func(name string) state.ObjectBucket {
		id := uuid.NewString()
		return state.ObjectBucket{ID: id, AccountID: acct.ID, AppID: app.ID, Name: name, Scope: "default", Region: "us-east-1", BackendID: "external", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", "")}
	}
	b, err := st.ReserveObjectBucket(ctx, makeBucket("assets"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != "provisioning" || b.CreatedAt.IsZero() {
		t.Fatal(b)
	}
	retry, err := st.ReserveObjectBucket(ctx, makeBucket("assets"), 3)
	if err != nil || retry.ID != b.ID {
		t.Fatal("reservation not idempotent", err)
	}
	if _, err = st.GetObjectBucket(ctx, uuid.NewString(), app.ID, b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("tenant leak", err)
	}
	if _, err = st.GetObjectBucket(ctx, acct.ID, uuid.NewString(), b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("app leak", err)
	}
	if err = base.DeleteApp(ctx, app.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("app deletion orphaned storage", err)
	}
	if err = base.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if err = base.DeleteAccount(ctx, acct.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("account deletion orphaned storage", err)
	}
	if err = base.RestoreAccount(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "first", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "second", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("double lease", err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "second", "ready"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unowned completion", err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "first", "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "third", "provisioning"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("ready bucket reprovisioned", err)
	}
	// Distinct concurrent names must share one per-app quota, across scopes.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := st.ReserveObjectBucket(ctx, makeBucket(fmt.Sprintf("bucket-%d", i)), 3)
			if err != nil && !errors.Is(err, state.ErrConflict) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	items, err := st.ListObjectBuckets(ctx, acct.ID, app.ID)
	if err != nil || len(items) != 3 {
		t.Fatal("quota race", len(items), err)
	}
	for _, item := range items {
		if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, item.ID, "delete", "deleting"); err != nil {
			t.Fatal(err)
		}
		if err = st.FinishObjectBucket(ctx, item.ID, "delete", "deleted"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = st.GetObjectBucket(ctx, acct.ID, app.ID, b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("deleted bucket visible", err)
	}
	newBucket, err := st.ReserveObjectBucket(ctx, makeBucket("assets"), 3)
	if err != nil || newBucket.PhysicalName == b.PhysicalName {
		t.Fatal("reused physical bucket", err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, newBucket.ID, "delete", "deleting"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, newBucket.ID, "delete", "deleted"); err != nil {
		t.Fatal(err)
	}
	if err = base.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ReserveObjectBucket(ctx, makeBucket("after-delete"), 3); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("reserved on deleted app", err)
	}
	if err = base.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if err = base.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatal("deleted bucket tombstones blocked account cleanup", err)
	}
}

func TestObjectBucketLeaseRecoveryPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	acct, err := st.CreateAccount(ctx, "lease@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "lease-app", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: "external", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-lease-test"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "old", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_buckets SET lease_until=now()-interval '1 second' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "new", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "old", "ready"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker completed", err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "new", "ready"); err != nil {
		t.Fatal(err)
	}
}
