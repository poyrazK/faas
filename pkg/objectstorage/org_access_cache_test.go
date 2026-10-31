// adr: 20261030075301

package objectstorage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeMemberships struct {
	mu       sync.Mutex
	revision map[string]int64
	readErr  error
	reads    int
}

func (f *fakeMemberships) OrgAccessSnapshot(_ context.Context, org, account string) (state.OrgAccessSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.readErr != nil {
		return state.OrgAccessSnapshot{}, f.readErr
	}
	revision, ok := f.revision[org+"/"+account]
	if !ok {
		return state.OrgAccessSnapshot{}, state.ErrNotFound
	}
	return state.OrgAccessSnapshot{Role: state.OrgRoleDeveloper, Revision: revision}, nil
}

func (f *fakeMemberships) set(fn func(*fakeMemberships)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func TestOrgAccessCacheChecksFences(t *testing.T) {
	members := &fakeMemberships{revision: map[string]int64{"org/member": 3}}
	clock := &fakeClock{now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	c := NewOrgAccessCache(members, clock.Now)
	ctx := t.Context()
	fence := state.OrgAccessFence{OrgID: "org", AccountID: "member", Revision: 3}
	for range 3 {
		if err := c.Check(ctx, fence); err != nil {
			t.Fatal(err)
		}
	}
	if members.reads != 1 {
		t.Fatalf("three checks within the TTL made %d reads", members.reads)
	}
	old := fence
	old.Revision = 2
	if err := c.Check(ctx, old); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("a credential from an older membership revision = %v", err)
	}
	if err := c.Check(ctx, state.OrgAccessFence{OrgID: "org", AccountID: "stranger", Revision: 3}); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("a non-member = %v", err)
	}

	members.set(func(f *fakeMemberships) { delete(f.revision, "org/member") })
	if err := c.Check(ctx, fence); err != nil {
		t.Fatal("a removal reached reads before the TTL", err)
	}
	clock.Add(api.ObjectReadAdmissionTTL)
	if err := c.Check(ctx, fence); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("a removal did not reach reads after the TTL: %v", err)
	}
}

func TestOrgAccessCacheOutages(t *testing.T) {
	members := &fakeMemberships{revision: map[string]int64{"org/member": 1}}
	clock := &fakeClock{now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	c := NewOrgAccessCache(members, clock.Now)
	ctx := t.Context()
	fence := state.OrgAccessFence{OrgID: "org", AccountID: "member", Revision: 1}
	if err := c.Check(ctx, fence); err != nil {
		t.Fatal(err)
	}
	members.set(func(f *fakeMemberships) { f.readErr = errors.New("database unreachable") })
	clock.Add(api.ObjectReadAdmissionMaxStale - time.Second)
	if err := c.Check(ctx, fence); err != nil {
		t.Fatal("an outage within the stale window refused a member", err)
	}
	clock.Add(time.Second)
	if err := c.Check(ctx, fence); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("an outage past the stale window = %v", err)
	}
	cold := NewOrgAccessCache(members, clock.Now)
	if err := cold.Check(ctx, fence); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("an outage with nothing cached = %v", err)
	}
}

func TestOrgAccessCacheSharesOneReadAcrossConcurrentMisses(t *testing.T) {
	members := &fakeMemberships{revision: map[string]int64{"org/member": 1}}
	c := NewOrgAccessCache(members, nil)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Check(t.Context(), state.OrgAccessFence{OrgID: "org", AccountID: "member", Revision: 1}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if members.reads != 1 {
		t.Fatalf("20 concurrent misses made %d reads", members.reads)
	}
}

// TestReadDecisionIgnoresTheCallersFence: a bucket's decision is shared by
// every credential, so a refresh triggered by a revoked credential must not
// fail it for the others (ADR-20261030075301 step 3).
func TestReadDecisionIgnoresTheCallersFence(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "fenced-read@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "fenced-read", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	since := time.Now().UTC().Add(-time.Minute)
	policy := api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since, MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1 << 30, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	bucket, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: "test", BackendFingerprint: "fp", PhysicalName: "gregale-fenced"}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "token", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishObjectBucket(ctx, bucket.ID, "token", "ready"); err != nil {
		t.Fatal(err)
	}
	if err = store.ClaimObjectInventory(ctx, bucket.ID, "inventory"); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishObjectInventory(ctx, bucket.ID, "inventory", 0, 0); err != nil {
		t.Fatal(err)
	}
	revoked, release := state.WithOrgAccessFence(ctx, state.OrgAccessFence{OrgID: uuid.NewString(), AccountID: uuid.NewString(), Revision: 1})
	defer release(nil)
	if _, err = NewReadAdmitter(store, policy, nil).AdmitRead(revoked, bucket); err != nil {
		t.Fatal("a revoked caller's fence failed the bucket's shared decision", err)
	}
}
