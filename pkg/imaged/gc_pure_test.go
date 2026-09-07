// gc_pure_test.go — fill the remaining pkg/imaged/gc.go coverage gaps that
// loop_test.go's high-level integration tests don't deeply reach. Targets:
//
//   - perAppKeepTierFloor — every branch in the tier-policy loop (warmEnabled
//     on/off, warm-tier protected rows, init-tier protected rows, mixed
//     apps in one input slice, identical-CreatedAt F-09 tiebreaker, empty
//     input).
//   - perAppEvictionCandidates — direct whitebox sweep (currently only
//     reached transitively from evictOldestFromHeaviestAccount via Loop).
//   - evictOldestFromHeaviestAccount — heavy-account selection, ties on
//     total bytes, empty input, no-candidates branch (every row is
//     protected by per-tier floor), single-eviction contract.
//
// Conventions: whitebox `package imaged` (matches the pre-existing
// loop_test.go which is also whitebox). Reuses the state.SnapshotForGC
// struct from pkg/state (no extra fixtures).

package imaged

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// row is a tiny constructor for state.SnapshotForGC to keep the table
// tests below readable. The defaults (MemBytes=1024, DiskBytes=2048,
// FCVersion="1.10") match what existing loop_test.go tests construct.
func row(id, app, dep, account, slug, tier string, warmEnabled bool, createdAt time.Time, mem, disk int64) state.SnapshotForGC {
	return state.SnapshotForGC{
		ID:                     id,
		DeploymentID:           dep,
		AppID:                  app,
		AccountID:              account,
		AppSlug:                slug,
		FCVersion:              "1.10",
		MemBytes:               mem,
		DiskBytes:              disk,
		Tier:                   tier,
		AppWarmSnapshotEnabled: warmEnabled,
		CreatedAt:              createdAt,
	}
}

// collectIDs returns just the IDs of the drop targets, sorted, for
// stable comparison (the function's output order is deterministic but
// the test should not depend on map-iteration order).
func collectIDs(targets []deleteTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.ID)
	}
	return out
}

func sortedStringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sortStrings(ac)
	sortStrings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

func TestPerAppKeepTierFloor_DropsUnusableSnapshotsBeforeFloor(t *testing.T) {
	now := time.Now()
	failed := row("failed", "app", "dep-failed", "acct", "slug", state.SnapshotTierInit, false, now, 1, 1)
	failed.DeploymentStatus = state.DeployFailed
	cancelled := row("cancelled", "app", "dep-cancelled", "acct", "slug", state.SnapshotTierInit, false, now, 1, 1)
	cancelled.DeploymentStatus = state.DeployCancelled
	deleted := row("deleted", "deleted-app", "dep-deleted", "acct", "deleted-slug", state.SnapshotTierInit, false, now, 1, 1)
	deleted.AppStatus = state.AppDeleted
	live := row("live", "app", "dep-live", "acct", "slug", state.SnapshotTierInit, false, now.Add(-time.Minute), 1, 1)
	live.DeploymentStatus = state.DeployLive
	previous := row("previous", "app", "dep-previous", "acct", "slug", state.SnapshotTierInit, false, now.Add(-2*time.Minute), 1, 1)
	previous.DeploymentStatus = state.DeploySuperseded

	got := collectIDs(perAppKeepTierFloor([]state.SnapshotForGC{failed, cancelled, deleted, live, previous}))
	want := []string{"failed", "cancelled", "deleted"}
	if !sortedStringsEqual(got, want) {
		t.Fatalf("drop IDs = %v, want %v", got, want)
	}
}

// sortStrings is a tiny inlined helper to avoid importing "sort" just
// for a single ascending sort in tests.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// --- perAppKeepTierFloor (gc.go:64) ---------------------------------

func TestPerAppKeepTierFloor_Empty(t *testing.T) {
	// Pin the empty-input short-circuit.
	got := perAppKeepTierFloor(nil)
	if len(got) != 0 {
		t.Errorf("empty input: got %v, want nil/empty", got)
	}
}

func TestPerAppKeepTierFloor_AllWithinFloor_NoDrops(t *testing.T) {
	// Warm-enabled app with ≤2 warm + ≤2 init rows → nothing dropped.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("w1", "app-A", "dep-1", "acct-1", "app-a", state.SnapshotTierWarm, true, t0, 100, 200),
		row("i1", "app-A", "dep-1", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 200),
		row("i2", "app-A", "dep-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(2*time.Second), 100, 200),
	}
	got := perAppKeepTierFloor(rows)
	if len(got) != 0 {
		t.Errorf("≤2 warm + ≤2 init: got drops=%v, want none", got)
	}
}

func TestPerAppKeepTierFloor_WarmEnabled_Keeps2Plus2(t *testing.T) {
	// warm_enabled=true with 3 warm + 3 init rows → drop 1 warm + 1 init.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("w-newest", "app-A", "dep-3", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(5*time.Second), 100, 200),
		row("w-mid", "app-A", "dep-2", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(3*time.Second), 100, 200),
		row("w-oldest", "app-A", "dep-1", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(time.Second), 100, 200),
		row("i-newest", "app-A", "dep-3", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(6*time.Second), 100, 200),
		row("i-mid", "app-A", "dep-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(4*time.Second), 100, 200),
		row("i-oldest", "app-A", "dep-1", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(2*time.Second), 100, 200),
	}
	got := perAppKeepTierFloor(rows)
	ids := collectIDs(got)
	want := []string{"w-oldest", "i-oldest"}
	if !sortedStringsEqual(ids, want) {
		t.Errorf("warm-enabled 3+3 floor drops: got %v, want %v", ids, want)
	}
}

func TestPerAppKeepTierFloor_WarmDisabled_KeepsOnlyInit(t *testing.T) {
	// warm_enabled=false → all warm rows dropped (vestigial), keep 2 init.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("w1", "app-A", "dep-1", "acct-1", "app-a", state.SnapshotTierWarm, false, t0, 100, 200),
		row("w2", "app-A", "dep-2", "acct-1", "app-a", state.SnapshotTierWarm, false, t0.Add(time.Second), 100, 200),
		row("i-newest", "app-A", "dep-3", "acct-1", "app-a", state.SnapshotTierInit, false, t0.Add(3*time.Second), 100, 200),
		row("i-mid", "app-A", "dep-2", "acct-1", "app-a", state.SnapshotTierInit, false, t0.Add(2*time.Second), 100, 200),
		row("i-oldest", "app-A", "dep-1", "acct-1", "app-a", state.SnapshotTierInit, false, t0.Add(time.Second), 100, 200),
	}
	got := perAppKeepTierFloor(rows)
	ids := collectIDs(got)
	want := []string{"w1", "w2", "i-oldest"}
	if !sortedStringsEqual(ids, want) {
		t.Errorf("warm-disabled drops: got %v, want %v", ids, want)
	}
}

func TestPerAppKeepTierFloor_MixedApps_Isolated(t *testing.T) {
	// Two apps in one slice → each app's policy is independent.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("A-1", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierInit, true, t0, 100, 200),
		row("A-2", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 200),
		row("A-3", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(2*time.Second), 100, 200),
		row("B-1", "app-B", "d-1", "acct-1", "app-b", state.SnapshotTierWarm, true, t0, 100, 200),
		row("B-2", "app-B", "d-2", "acct-1", "app-b", state.SnapshotTierWarm, true, t0.Add(time.Second), 100, 200),
		row("B-3", "app-B", "d-3", "acct-1", "app-b", state.SnapshotTierWarm, true, t0.Add(2*time.Second), 100, 200),
	}
	got := perAppKeepTierFloor(rows)
	ids := collectIDs(got)
	// App-A: 3 init rows → drop oldest (A-1).
	// App-B: 3 warm rows → drop oldest (B-1).
	want := []string{"A-1", "B-1"}
	if !sortedStringsEqual(ids, want) {
		t.Errorf("mixed-apps isolation: got %v, want %v", ids, want)
	}
}

func TestPerAppKeepTierFloor_IdenticalCreatedAt_F09Tiebreaker(t *testing.T) {
	// F-09 regression: identical CreatedAt must use ID asc tiebreaker so
	// the deterministic "smaller id is newer" rule kicks in. With 3 init
	// rows at the same timestamp + IDs in non-sorted order, the function
	// must drop "id-3" (the largest id, which the tiebreaker treats as
	// oldest).
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("id-2", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0, 100, 200),
		row("id-3", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierInit, true, t0, 100, 200),
		row("id-1", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierInit, true, t0, 100, 200),
	}
	got := perAppKeepTierFloor(rows)
	ids := collectIDs(got)
	if len(ids) != 1 || ids[0] != "id-3" {
		t.Errorf("F-09 tiebreaker: got %v, want [id-3] (largest ID treated as oldest)", ids)
	}
}

func TestPerAppKeepTierFloor_DropTargetShape(t *testing.T) {
	// Pin the shape of each deleteTarget — ID, DeploymentID, AppSlug, Tier
	// must all be projected from the row (B1.1: no re-resolve).
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("drop-1", "app-A", "dep-X", "acct-1", "slug-A", state.SnapshotTierWarm, true, t0, 100, 200),
		row("keep-1", "app-A", "dep-Y", "acct-1", "slug-A", state.SnapshotTierWarm, true, t0.Add(time.Second), 100, 200),
		row("keep-2", "app-A", "dep-Z", "acct-1", "slug-A", state.SnapshotTierWarm, true, t0.Add(2*time.Second), 100, 200),
	}
	got := perAppKeepTierFloor(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 drop, got %d: %v", len(got), got)
	}
	d := got[0]
	if d.ID != "drop-1" || d.DeploymentID != "dep-X" || d.AppSlug != "slug-A" || d.Tier != state.SnapshotTierWarm {
		t.Errorf("dropTarget shape: got %+v, want {drop-1 dep-X slug-A warm}", d)
	}
}

// --- perAppEvictionCandidates (gc.go:241) --------------------------

func TestPerAppEvictionCandidates_Empty(t *testing.T) {
	got := perAppEvictionCandidates(nil, true)
	if len(got) != 0 {
		t.Errorf("empty input warm-enabled: got %v, want empty", got)
	}
	got = perAppEvictionCandidates(nil, false)
	if len(got) != 0 {
		t.Errorf("empty input warm-disabled: got %v, want empty", got)
	}
}

func TestPerAppEvictionCandidates_WarmEnabled_BelowFloor_NoEvictions(t *testing.T) {
	// ≤2 warm + ≤2 init → no candidates.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("w1", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierWarm, true, t0, 100, 200),
		row("i1", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 200),
	}
	got := perAppEvictionCandidates(rows, true)
	if len(got) != 0 {
		t.Errorf("below floor warm-enabled: got %v, want empty", got)
	}
}

func TestPerAppEvictionCandidates_WarmEnabled_AboveFloor_AllTiersPooled(t *testing.T) {
	// warm_enabled=true with 3 warm + 3 init → drop 1 warm + 1 init, oldest
	// first across the merged pool.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("w-newest", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(5*time.Second), 100, 200),
		row("w-mid", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(3*time.Second), 100, 200),
		row("w-oldest", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(time.Second), 100, 200),
		row("i-newest", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(6*time.Second), 100, 200),
		row("i-mid", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(4*time.Second), 100, 200),
		row("i-oldest", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(2*time.Second), 100, 200),
	}
	got := perAppEvictionCandidates(rows, true)
	if len(got) != 2 {
		t.Fatalf("warm-enabled 3+3: got %d candidates, want 2", len(got))
	}
	// candidates[0] must be the oldest across both pools = w-oldest (t0+1s).
	if got[0].ID != "w-oldest" {
		t.Errorf("warm-enabled oldest-first: got %s, want w-oldest", got[0].ID)
	}
	// Second candidate must be i-oldest (t0+2s).
	if got[1].ID != "i-oldest" {
		t.Errorf("warm-enabled second-oldest: got %s, want i-oldest", got[1].ID)
	}
}

func TestPerAppEvictionCandidates_WarmDisabled_AllWarmEvictable(t *testing.T) {
	// warm_enabled=false with 2 warm + 3 init → all warm are evictable
	// (vestigial), plus 1 init. Pool sorted oldest-first across tiers.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("w-oldest", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierWarm, false, t0, 100, 200),
		row("w-newer", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierWarm, false, t0.Add(time.Second), 100, 200),
		row("i-newest", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierInit, false, t0.Add(3*time.Second), 100, 200),
		row("i-mid", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, false, t0.Add(2*time.Second), 100, 200),
		row("i-oldest", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierInit, false, t0.Add(time.Second), 100, 200),
	}
	got := perAppEvictionCandidates(rows, false)
	// Tie at t0+1s (w-newer and i-oldest): tiebreaker is ID asc, so
	// "i-oldest" comes before "w-newer".
	wantOrder := []string{"w-oldest", "i-oldest", "w-newer"}
	if len(got) != len(wantOrder) {
		t.Fatalf("warm-disabled: got %d, want %d (%v)", len(got), len(wantOrder), got)
	}
	for i, w := range wantOrder {
		if got[i].ID != w {
			t.Errorf("warm-disabled[%d]: got %s, want %s", i, got[i].ID, w)
		}
	}
}

func TestPerAppEvictionCandidates_UnknownTier_TreatedAsInit(t *testing.T) {
	// The default branch in the switch treats unknown tier as init (the
	// "default:" arm). Pin that an unrecognized tier string still keeps
	// the per-tier floor.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("x-newest", "app-A", "d-3", "acct-1", "app-a", "unknown-tier", true, t0.Add(2*time.Second), 100, 200),
		row("x-mid", "app-A", "d-2", "acct-1", "app-a", "unknown-tier", true, t0.Add(time.Second), 100, 200),
		row("x-oldest", "app-A", "d-1", "acct-1", "app-a", "unknown-tier", true, t0, 100, 200),
	}
	got := perAppEvictionCandidates(rows, true)
	if len(got) != 1 || got[0].ID != "x-oldest" {
		t.Errorf("unknown-tier as init: got %v, want [x-oldest]", got)
	}
}

// --- evictOldestFromHeaviestAccount (gc.go:144) --------------------

func TestEvictOldestFromHeaviestAccount_EmptyReturnsNil(t *testing.T) {
	got := evictOldestFromHeaviestAccount(nil)
	if got != nil {
		t.Errorf("empty input: got %v, want nil", got)
	}
}

func TestEvictOldestFromHeaviestAccount_AllProtected_NoCandidates(t *testing.T) {
	// All rows within per-tier floor → no eviction candidate.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("w1", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierWarm, true, t0, 100, 200),
		row("i1", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 200),
	}
	got := evictOldestFromHeaviestAccount(rows)
	if got != nil {
		t.Errorf("all-protected: got %v, want nil", got)
	}
}

func TestEvictOldestFromHeaviestAccount_PicksHeaviestAccount(t *testing.T) {
	// Two accounts; the heavier (acct-2) wins even when its rows are newer.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		// acct-1: light account, older rows.
		row("a1-1", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierInit, true, t0, 100, 100),
		row("a1-2", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 100),
		row("a1-3", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(2*time.Second), 100, 100),
		// acct-2: heavy account (10× larger mem), newer rows.
		row("a2-1", "app-B", "d-1", "acct-2", "app-b", state.SnapshotTierInit, true, t0.Add(3*time.Second), 10000, 100),
		row("a2-2", "app-B", "d-2", "acct-2", "app-b", state.SnapshotTierInit, true, t0.Add(4*time.Second), 10000, 100),
		row("a2-3", "app-B", "d-3", "acct-2", "app-b", state.SnapshotTierInit, true, t0.Add(5*time.Second), 10000, 100),
	}
	got := evictOldestFromHeaviestAccount(rows)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// The heavy account is acct-2; its oldest evictable row is a2-1
	// (the per-tier floor keeps the 2 newest, so a2-1 is dropped).
	if got[0].ID != "a2-1" {
		t.Errorf("heavy-account pick: got %s, want a2-1", got[0].ID)
	}
	if got[0].Tier != state.SnapshotTierInit {
		t.Errorf("tier: got %s, want init", got[0].Tier)
	}
}

func TestEvictOldestFromHeaviestAccount_AccountTieBreakerOnBytes(t *testing.T) {
	// Two accounts with identical total bytes → pick the smaller account id
	// (the sort tiebreaker at gc.go:162-166).
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("z-1", "app-A", "d-1", "acct-Z", "app-a", state.SnapshotTierInit, true, t0, 500, 500),
		row("z-2", "app-A", "d-2", "acct-Z", "app-a", state.SnapshotTierInit, true, t0.Add(time.Second), 500, 500),
		row("z-3", "app-A", "d-3", "acct-Z", "app-a", state.SnapshotTierInit, true, t0.Add(2*time.Second), 500, 500),
		row("a-1", "app-B", "d-1", "acct-A", "app-b", state.SnapshotTierInit, true, t0, 500, 500),
		row("a-2", "app-B", "d-2", "acct-A", "app-b", state.SnapshotTierInit, true, t0.Add(time.Second), 500, 500),
		row("a-3", "app-B", "d-3", "acct-A", "app-b", state.SnapshotTierInit, true, t0.Add(2*time.Second), 500, 500),
	}
	got := evictOldestFromHeaviestAccount(rows)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// acct-A wins (smaller id, tiebreaker). The deleteTarget struct
	// doesn't carry AccountID — the caller resolves it via the
	// returned ID. Verify the picked row corresponds to acct-A's
	// oldest (a-1).
	if got[0].ID != "a-1" {
		t.Errorf("oldest in heavy account: got %s, want a-1", got[0].ID)
	}
}

func TestEvictOldestFromHeaviestAccount_OldestAcrossAppsPerAccount(t *testing.T) {
	// Heavy account has two apps, each with rows above the floor; the
	// function must pick the single OLDEST across both apps.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		// App-A in heavy acct: 3 init, oldest is a-1.
		row("a-1", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierInit, true, t0, 100, 200),
		row("a-2", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 200),
		row("a-3", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(2*time.Second), 100, 200),
		// App-B in heavy acct: 3 init, oldest is b-1 (older than a-1).
		row("b-1", "app-B", "d-1", "acct-1", "app-b", state.SnapshotTierInit, true, t0.Add(-time.Second), 100, 200),
		row("b-2", "app-B", "d-2", "acct-1", "app-b", state.SnapshotTierInit, true, t0, 100, 200),
		row("b-3", "app-B", "d-3", "acct-1", "app-b", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 200),
	}
	got := evictOldestFromHeaviestAccount(rows)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// b-1 is older than a-1 by 1s; it must win.
	if got[0].ID != "b-1" {
		t.Errorf("cross-app oldest: got %s, want b-1", got[0].ID)
	}
}

func TestEvictOldestFromHeaviestAccount_SingleEviction_NotArray(t *testing.T) {
	// Contract: returns AT MOST ONE deleteTarget. Even when many apps have
	// evictable rows, the function picks exactly one (the oldest) so the
	// caller can loop until pressure is relieved.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("a-1", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierInit, true, t0, 100, 200),
		row("a-2", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 200),
		row("a-3", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierInit, true, t0.Add(2*time.Second), 100, 200),
		row("b-1", "app-B", "d-1", "acct-1", "app-b", state.SnapshotTierInit, true, t0.Add(-time.Second), 100, 200),
		row("b-2", "app-B", "d-2", "acct-1", "app-b", state.SnapshotTierInit, true, t0, 100, 200),
		row("b-3", "app-B", "d-3", "acct-1", "app-b", state.SnapshotTierInit, true, t0.Add(time.Second), 100, 200),
	}
	got := evictOldestFromHeaviestAccount(rows)
	if len(got) > 1 {
		t.Errorf("single-eviction contract: got %d, want ≤1", len(got))
	}
}

func TestEvictOldestFromHeaviestAccount_TierInOutput(t *testing.T) {
	// The output's Tier field must reflect the actual tier of the
	// evicted row (warm vs init).
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("w1", "app-A", "d-1", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(-time.Second), 100, 200),
		row("w2", "app-A", "d-2", "acct-1", "app-a", state.SnapshotTierWarm, true, t0, 100, 200),
		row("w3", "app-A", "d-3", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(time.Second), 100, 200),
		row("w4", "app-A", "d-4", "acct-1", "app-a", state.SnapshotTierWarm, true, t0.Add(2*time.Second), 100, 200),
	}
	got := evictOldestFromHeaviestAccount(rows)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Tier != state.SnapshotTierWarm {
		t.Errorf("tier in output: got %s, want warm", got[0].Tier)
	}
	if got[0].ID != "w1" {
		t.Errorf("oldest warm: got %s, want w1", got[0].ID)
	}
}
