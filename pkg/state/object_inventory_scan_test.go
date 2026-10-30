// adr: 20261030100835

package state_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectInventoryScan covers a listing that spans attempts: only a claim
// that starts or publishes it renews the attempt time deletions yield to, an
// unfinished listing is due at once, a listing older than the bucket's
// observation starts again, and publication is observed as of the start.
func TestObjectInventoryScanMem(t *testing.T) { objectInventoryScanSuite(t, state.NewMemStore()) }
func TestObjectInventoryScanPG(t *testing.T) {
	st, _ := pgStore(t)
	objectInventoryScanSuite(t, st)
}

func objectInventoryScanSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	usage := func() state.ObjectBucketUsage {
		t.Helper()
		s, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if err != nil || len(s.Buckets) != 1 {
			t.Fatal(s, err)
		}
		return s.Buckets[0]
	}
	due := func() bool {
		t.Helper()
		rows, err := st.DueObjectInventories(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(rows, func(r state.ObjectBucket) bool { return r.ID == b.ID })
	}
	seeded := usage().ObservedAt

	first := uuid.NewString()
	scan, err := st.ClaimObjectInventoryScan(ctx, b.ID, first)
	if err != nil || scan.Pages != 0 || !scan.StartedAt.After(seeded) {
		t.Fatal("start claim", scan, err)
	}
	if u := usage(); !u.AttemptAt.Equal(scan.StartedAt) {
		t.Fatal("a claim that starts a listing did not renew the attempt time", u.AttemptAt, scan.StartedAt)
	}
	if _, err = st.ClaimObjectInventoryScan(ctx, b.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("second claim while leased", err)
	}
	scan.After, scan.Bytes, scan.Keys, scan.Pages = "k2", 20, 2, 2
	if err = st.CheckpointObjectInventoryScan(ctx, b.ID, first, scan); err != nil {
		t.Fatal(err)
	}
	u := usage()
	if u.Token != "" || u.InventoryScan.Pages != 2 || u.InventoryScan.Bytes != 20 {
		t.Fatal("checkpoint", u)
	}
	if got := state.SummarizeObjectUsage(state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{u}}, accountingPolicy(), time.Now()); got.CapacityBytes != 20 || got.CapacityKeys != 2 {
		t.Fatal("listed bytes do not count toward capacity", got)
	}
	if !due() {
		t.Fatal("an unfinished listing is not due at once")
	}

	second := uuid.NewString()
	resumed, err := st.ClaimObjectInventoryScan(ctx, b.ID, second)
	if err != nil || !resumed.StartedAt.Equal(scan.StartedAt) || resumed.After != "k2" || resumed.Pages != 2 || resumed.Bytes != 20 {
		t.Fatal("continue claim", resumed, err)
	}
	if u := usage(); !u.AttemptAt.Equal(scan.StartedAt) {
		t.Fatal("a claim that continues a listing renewed the attempt time", u.AttemptAt)
	}
	backwards := resumed
	backwards.Pages = 1
	if err = st.CheckpointObjectInventoryScan(ctx, b.ID, second, backwards); !errors.Is(err, state.ErrConflict) {
		t.Fatal("checkpoint went backwards", err)
	}
	if err = st.CheckpointObjectInventoryScan(ctx, b.ID, first, resumed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("checkpoint under a released claim", err)
	}
	resumed.After, resumed.Bytes, resumed.Keys, resumed.Pages, resumed.Complete = "k3", 30, 3, 3, true
	if err = st.FinishObjectInventoryScan(ctx, b.ID, second, resumed); err != nil {
		t.Fatal(err)
	}
	if u := usage(); u.ObservedBytes != 30 || u.ObservedKeys != 3 || !u.ObservedAt.Equal(scan.StartedAt) || u.InventoryScan.Pages != 0 || u.Token != "" {
		t.Fatal("publication", u)
	}

	// A newer observation from a release that lists in one attempt
	// supersedes a listing in progress.
	third := uuid.NewString()
	stale, err := st.ClaimObjectInventoryScan(ctx, b.ID, third)
	if err != nil {
		t.Fatal(err)
	}
	stale.Pages, stale.After, stale.Keys = 1, "a", 1
	if err = st.CheckpointObjectInventoryScan(ctx, b.ID, third, stale); err != nil {
		t.Fatal(err)
	}
	legacy := uuid.NewString()
	if err = st.ClaimObjectInventory(ctx, b.ID, legacy); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, legacy, 5, 1); err != nil {
		t.Fatal(err)
	}
	if due() {
		t.Fatal("a superseded listing kept its bucket due")
	}
	restarted, err := st.ClaimObjectInventoryScan(ctx, b.ID, uuid.NewString())
	if err != nil || restarted.Pages != 0 || !restarted.StartedAt.After(usage().ObservedAt) {
		t.Fatal("a superseded listing was continued", restarted, err)
	}
}

// TestObjectInventoryScanPublicationWaitsForDeletions: publication still
// refuses while a deletion is unsettled, so a complete listing is kept and
// published by a later claim that makes deletions yield again.
func TestObjectInventoryScanPublicationWaitsForDeletionsMem(t *testing.T) {
	objectInventoryScanDeletionSuite(t, state.NewMemStore())
}
func TestObjectInventoryScanPublicationWaitsForDeletionsPG(t *testing.T) {
	st, _ := pgStore(t)
	objectInventoryScanDeletionSuite(t, st)
}

func objectInventoryScanDeletionSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	if _, created, err := st.(state.ObjectDeletionStore).BeginObjectDeletion(ctx, deletionCaptureInput(b), accountingPolicy()); err != nil || !created {
		t.Fatal(err)
	}
	token := uuid.NewString()
	scan, err := st.ClaimObjectInventoryScan(ctx, b.ID, token)
	if err != nil {
		t.Fatal(err)
	}
	scan.Pages, scan.Complete = 1, true
	if err = st.FinishObjectInventoryScan(ctx, b.ID, token, scan); !errors.Is(err, state.ErrConflict) {
		t.Fatal("published beside an unsettled deletion", err)
	}
	if err = st.CheckpointObjectInventoryScan(ctx, b.ID, token, scan); err != nil {
		t.Fatal(err)
	}
	before, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	publish, err := st.ClaimObjectInventoryScan(ctx, b.ID, uuid.NewString())
	if err != nil || !publish.Complete || !publish.StartedAt.Equal(scan.StartedAt) {
		t.Fatal("publish claim", publish, err)
	}
	after, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || !after.Buckets[0].AttemptAt.After(before.Buckets[0].AttemptAt) {
		t.Fatal("a claim that publishes did not renew the attempt time", err)
	}
}

// TestObjectInventoryScanFreshness: a bucket whose listing is advancing stays
// fresh on an observation up to ObjectInventoryScanMaxAge old, and what the
// listing has seen counts toward capacity.
func TestObjectInventoryScanFreshness(t *testing.T) {
	now := time.Now().UTC()
	for _, mode := range []struct {
		name   string
		policy func() api.ObjectStoragePolicy
	}{
		{"provider reports", planCeilingPolicy},
		{"gateway safety", gatewaySafetyPolicy},
	} {
		for _, tc := range []struct {
			name     string
			observed time.Duration
			scan     state.ObjectInventoryScan
			put      int64
			want     error
		}{
			{name: "fresh without a listing", observed: time.Minute, put: 1},
			{name: "stale without a listing", observed: 20 * time.Minute, put: 1, want: state.ErrObjectUsageStale},
			{name: "advancing listing keeps a stale bucket", observed: 20 * time.Minute, scan: state.ObjectInventoryScan{StartedAt: now.Add(-10 * time.Minute), ProgressAt: now.Add(-time.Minute), Pages: 4}, put: 1},
			{name: "stalled listing does not", observed: 20 * time.Minute, scan: state.ObjectInventoryScan{StartedAt: now.Add(-10 * time.Minute), ProgressAt: now.Add(-api.ObjectInventoryScanProgressWindow - time.Second), Pages: 4}, put: 1, want: state.ErrObjectUsageStale},
			{name: "listing without a page does not", observed: 20 * time.Minute, scan: state.ObjectInventoryScan{StartedAt: now.Add(-time.Minute), ProgressAt: now.Add(-time.Minute)}, put: 1, want: state.ErrObjectUsageStale},
			{name: "observation past the listing limit", observed: api.ObjectInventoryScanMaxAge + time.Minute, scan: state.ObjectInventoryScan{StartedAt: now.Add(-10 * time.Minute), ProgressAt: now.Add(-time.Minute), Pages: 4}, put: 1, want: state.ErrObjectUsageStale},
			{name: "listing older than the observation does not", observed: 20 * time.Minute, scan: state.ObjectInventoryScan{StartedAt: now.Add(-30 * time.Minute), ProgressAt: now.Add(-time.Minute), Pages: 4}, put: 1, want: state.ErrObjectUsageStale},
			{name: "listed bytes count toward capacity", observed: 20 * time.Minute, scan: state.ObjectInventoryScan{StartedAt: now.Add(-10 * time.Minute), ProgressAt: now.Add(-time.Minute), Pages: 4, Bytes: 95, Keys: 1}, put: 10, want: state.ErrObjectCapacity},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				s := twoBucketSnapshot(now, 0, 0, now.Add(-tc.observed))
				s.Buckets[1].InventoryScan = tc.scan
				_, _, err := state.CheckObjectAdmission(s, "b", tc.put, 0, false, true, mode.policy(), now)
				if !errors.Is(err, tc.want) && (tc.want != nil || err != nil) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			})
		}
	}
}
