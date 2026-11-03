// adr: 20261029174627

package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type placementStore = contractTestStore

func TestChooseObjectBucketPlacement(t *testing.T) {
	a := state.ObjectBucketPlacement{BackendID: "a", MaxBuckets: 2}
	b := state.ObjectBucketPlacement{BackendID: "b", MaxBuckets: 2}
	unlimited := state.ObjectBucketPlacement{BackendID: "u"}
	for _, tc := range []struct {
		name       string
		candidates []state.ObjectBucketPlacement
		counts     map[string]state.ObjectBucketPlacementCount
		want       string
	}{
		{"empty backends fill in order", []state.ObjectBucketPlacement{a, b}, nil, "a"},
		{"a full backend is skipped", []state.ObjectBucketPlacement{a, b}, map[string]state.ObjectBucketPlacementCount{"a": {Buckets: 2}}, "b"},
		{"the account's backend wins", []state.ObjectBucketPlacement{a, b}, map[string]state.ObjectBucketPlacementCount{"a": {Buckets: 1}, "b": {Buckets: 1, AccountBuckets: 1}}, "b"},
		{"the account's full backend gives way", []state.ObjectBucketPlacement{a, b}, map[string]state.ObjectBucketPlacementCount{"a": {Buckets: 1}, "b": {Buckets: 2, AccountBuckets: 2}}, "a"},
		{"most of the account's buckets wins", []state.ObjectBucketPlacement{a, b}, map[string]state.ObjectBucketPlacementCount{"a": {Buckets: 1, AccountBuckets: 1}, "b": {Buckets: 1, AccountBuckets: 1}}, "a"},
		{"no ceiling never fills", []state.ObjectBucketPlacement{unlimited}, map[string]state.ObjectBucketPlacementCount{"u": {Buckets: 1 << 40}}, "u"},
		{"every backend full", []state.ObjectBucketPlacement{a, b}, map[string]state.ObjectBucketPlacementCount{"a": {Buckets: 2}, "b": {Buckets: 3}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := state.ChooseObjectBucketPlacement(tc.candidates, tc.counts)
			if ok != (tc.want != "") || got.BackendID != tc.want {
				t.Fatalf("choice = %q, %v; want %q", got.BackendID, ok, tc.want)
			}
		})
	}
}

func TestObjectBucketPlacementMem(t *testing.T) { objectBucketPlacementSuite(t, state.NewMemStore()) }
func TestObjectBucketPlacementPG(t *testing.T) {
	st, _ := pgStore(t)
	objectBucketPlacementSuite(t, st)
}

func TestObjectBucketPlacementConcurrentMem(t *testing.T) {
	objectBucketPlacementRace(t, state.NewMemStore())
}

func TestObjectBucketPlacementConcurrentPG(t *testing.T) {
	st, _ := pgStore(t)
	objectBucketPlacementRace(t, st)
}

func placementApp(t *testing.T, st placementStore) (string, string) {
	t.Helper()
	ctx := context.Background()
	acct, err := st.CreateAccount(ctx, "placement-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "placement-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	return acct.ID, app.ID
}

// placementCandidates gives each test its own backend IDs, because PG tests
// share one database and placement counts every live bucket on a backend.
func placementCandidates(ceiling int64) []state.ObjectBucketPlacement {
	suffix := uuid.NewString()[:8]
	return []state.ObjectBucketPlacement{
		{BackendID: "ovh-a-" + suffix, BackendFingerprint: strings.Repeat("a", 64), MaxBuckets: ceiling},
		{BackendID: "ovh-b-" + suffix, BackendFingerprint: strings.Repeat("b", 64), MaxBuckets: ceiling},
	}
}

func placeBucket(st placementStore, account, app, name string, candidates []state.ObjectBucketPlacement) (state.ObjectBucket, bool, error) {
	id := uuid.NewString()
	return st.ReserveObjectBucketPlaced(context.Background(), state.ObjectBucket{ID: id, AccountID: account, AppID: app, Name: name, Scope: "default", Region: "us-east-1", PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", "")}, 10, candidates)
}

func objectBucketPlacementSuite(t *testing.T, st placementStore) {
	ctx := context.Background()
	candidates := placementCandidates(2)
	first, second := candidates[0], candidates[1]
	account, app := placementApp(t, st)
	other, otherApp := placementApp(t, st)
	expect := func(name string, b state.ObjectBucket, created bool, err error, want state.ObjectBucketPlacement) state.ObjectBucket {
		t.Helper()
		if err != nil || !created || b.BackendID != want.BackendID || b.BackendFingerprint != want.BackendFingerprint {
			t.Fatalf("%s placed on %q (created %v), %v; want %q", name, b.BackendID, created, err, want.BackendID)
		}
		return b
	}
	b, created, err := placeBucket(st, account, app, "one", candidates)
	one := expect("first bucket", b, created, err, first)
	b, created, err = placeBucket(st, other, otherApp, "x", candidates)
	expect("another account's first bucket", b, created, err, first)
	b, created, err = placeBucket(st, account, app, "two", candidates)
	expect("bucket after the first backend filled", b, created, err, second)
	b, created, err = placeBucket(st, account, app, "three", candidates)
	expect("bucket next to the account's newest", b, created, err, second)
	if _, _, err = placeBucket(st, account, app, "four", candidates); !errors.Is(err, state.ErrObjectStorageRegionFull) {
		t.Fatalf("full region err = %v", err)
	}
	again, created, err := placeBucket(st, account, app, "one", candidates)
	if err != nil || created || again.ID != one.ID || again.BackendID != first.BackendID {
		t.Fatalf("retried create = %+v, %v, %v", again, created, err)
	}
	counts, err := st.ObjectBucketPlacementCounts(ctx, account, []string{first.BackendID, second.BackendID})
	if err != nil || counts[first.BackendID] != (state.ObjectBucketPlacementCount{Buckets: 2, AccountBuckets: 1}) || counts[second.BackendID] != (state.ObjectBucketPlacementCount{Buckets: 2, AccountBuckets: 2}) {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
	if _, err = st.ClaimObjectBucket(ctx, account, app, one.ID, "delete", "deleting"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, one.ID, "delete", "deleted"); err != nil {
		t.Fatal(err)
	}
	b, created, err = placeBucket(st, account, app, "five", candidates)
	expect("bucket after a deletion freed room", b, created, err, first)
	if _, _, err = placeBucket(st, account, app, "six", nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("placement without candidates err = %v", err)
	}
}

// objectBucketPlacementRace creates buckets for many accounts at once: the
// region lock, not the per-app lock, must keep them under the ceiling.
func objectBucketPlacementRace(t *testing.T, st placementStore) {
	candidates := placementCandidates(3)[:1]
	const accounts = 8
	apps := make([][2]string, accounts)
	for i := range apps {
		apps[i][0], apps[i][1] = placementApp(t, st)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	placed, full := 0, 0
	for _, a := range apps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := placeBucket(st, a[0], a[1], "race", candidates)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				placed++
			case errors.Is(err, state.ErrObjectStorageRegionFull):
				full++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if placed != 3 || full != accounts-3 {
		t.Fatalf("placed %d and refused %d; want 3 and %d", placed, full, accounts-3)
	}
}
