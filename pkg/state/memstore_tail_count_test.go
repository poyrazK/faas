package state

// TailCount MemStore tests (issue #667 / ADR-078). Mirrors the
// FrameworkReadyAt test pattern (memstore_framework_ready_test.go):
// white-box package so the tests can call the unexported helpers
// directly. The pgstore equivalent lives in pgstore_tail_count_test.go
// (//go:build !no_pg + pgtest.Open) and covers the same shape.
//
// Why a separate file: the TailCount test surface is distinct from
// FrameworkReadyAt (different seed invariants, different staleness
// tolerance, different failure-mode wiring — see the documentation
// on BumpInstanceTailCount / DecrementInstanceTailCount in store.go).
// Splitting the tests keeps each file's narrative scope tight and
// makes the per-feature wiring diff reviewable in isolation.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// seedTailCountInstanceRow inserts a single instance row in the
// "running" state inside an app+deployment root. The IDs are
// fixed but distinct from the framework-ready seed so the two
// test files can run in parallel without fixture collisions.
func seedTailCountInstanceRow(t *testing.T, m *MemStore) Instance {
	t.Helper()
	ctx := context.Background()
	account, err := m.CreateAccount(ctx, "tail-count@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateApp(ctx, App{
		ID: "00000000-0000-0000-0000-0000000000d1", AccountID: account.ID,
		Slug: "tail-count-app", Type: AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
		Status: AppActive, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := m.CreateDeployment(ctx, Deployment{
		ID: "00000000-0000-0000-0000-0000000000e1", AppID: "00000000-0000-0000-0000-0000000000d1",
		ImageDigest: "sha256:tailcount", Status: DeployLive, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	ins, err := m.CreateInstance(ctx, "00000000-0000-0000-0000-0000000000d1",
		"00000000-0000-0000-0000-0000000000e1", "running", 256, "local-0", "")
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	return ins
}

// TestMemStore_BumpInstanceTailCount_AddsAndReturnsPostValue
// confirms the BumpInstanceTailCount happy path: a positive
// delta adds to the counter and the function returns the
// post-update value, and a follow-up read via InstanceByID
// observes the same value (so the schedd reaper's tail_count
// read matches the receipt path's view).
func TestMemStore_BumpInstanceTailCount_AddsAndReturnsPostValue(t *testing.T) {
	ctx := context.Background()
	m := newMemStoreForTest()
	ins := seedTailCountInstanceRow(t, m)

	// Fresh row starts at 0 (DEFAULT 0 from migration 00151).
	got, err := m.InstanceByID(ctx, ins.ID)
	if err != nil {
		t.Fatalf("read fresh: %v", err)
	}
	if got.TailCount != 0 {
		t.Errorf("fresh TailCount = %d, want 0", got.TailCount)
	}

	// Bump by 1: post = 1.
	post, err := m.BumpInstanceTailCount(ctx, ins.ID, 1)
	if err != nil {
		t.Fatalf("bump +1: %v", err)
	}
	if post != 1 {
		t.Errorf("BumpInstanceTailCount(+1) post = %d, want 1", post)
	}
	got, _ = m.InstanceByID(ctx, ins.ID)
	if got.TailCount != 1 {
		t.Errorf("post-read TailCount = %d, want 1", got.TailCount)
	}

	// Bump by 2: post = 3.
	post, err = m.BumpInstanceTailCount(ctx, ins.ID, 2)
	if err != nil {
		t.Fatalf("bump +2: %v", err)
	}
	if post != 3 {
		t.Errorf("BumpInstanceTailCount(+2) post = %d, want 3", post)
	}
	got, _ = m.InstanceByID(ctx, ins.ID)
	if got.TailCount != 3 {
		t.Errorf("post-read TailCount = %d, want 3", got.TailCount)
	}
}

// TestMemStore_DecrementInstanceTailCount_DecrementsAndFloors
// pins the canonical decrement path (issue #667 / ADR-078).
// Decrement is the host-side mirror of the runner's
// WaitGroup.Done() — every terminal-event receipt fires one
// decrement. The GREATEST(…, 0) SQL floor is mirrored here:
// a decrement on a counter at 0 leaves it at 0 (does NOT
// underflow to negative).
func TestMemStore_DecrementInstanceTailCount_DecrementsAndFloors(t *testing.T) {
	ctx := context.Background()
	m := newMemStoreForTest()
	ins := seedTailCountInstanceRow(t, m)

	// Seed: 3 active tails.
	if _, err := m.BumpInstanceTailCount(ctx, ins.ID, 3); err != nil {
		t.Fatalf("seed +3: %v", err)
	}

	// Decrement to 2.
	if err := m.DecrementInstanceTailCount(ctx, ins.ID, 1); err != nil {
		t.Fatalf("decrement: %v", err)
	}
	got, _ := m.InstanceByID(ctx, ins.ID)
	if got.TailCount != 2 {
		t.Errorf("post-decrement TailCount = %d, want 2", got.TailCount)
	}

	// Decrement to 1.
	if err := m.DecrementInstanceTailCount(ctx, ins.ID, 1); err != nil {
		t.Fatalf("decrement: %v", err)
	}
	got, _ = m.InstanceByID(ctx, ins.ID)
	if got.TailCount != 1 {
		t.Errorf("post-decrement TailCount = %d, want 1", got.TailCount)
	}

	// Drain to 0.
	if err := m.DecrementInstanceTailCount(ctx, ins.ID, 1); err != nil {
		t.Fatalf("decrement: %v", err)
	}
	got, _ = m.InstanceByID(ctx, ins.ID)
	if got.TailCount != 0 {
		t.Errorf("post-drain TailCount = %d, want 0", got.TailCount)
	}

	// Stray decrement — counter is at 0; the floor prevents
	// underflow. A stale receipt from a parked instance is the
	// expected source of such drift; the snapshotAndPark 5s
	// watchdog force-parks regardless.
	if err := m.DecrementInstanceTailCount(ctx, ins.ID, 1); err != nil {
		t.Fatalf("stray decrement: %v", err)
	}
	got, _ = m.InstanceByID(ctx, ins.ID)
	if got.TailCount != 0 {
		t.Errorf("stray decrement underflowed: TailCount = %d, want 0", got.TailCount)
	}
}

// TestMemStore_BumpInstanceTailCount_NegativeDeltaFloorsAtZero
// pins the BumpInstanceTailCount negative-delta floor. Mirrors
// the DecrementInstanceTailCount floor — a stale receipt with
// a negative delta on a counter at 0 leaves it at 0. (In
// practice the receipt path always calls
// DecrementInstanceTailCount for terminal events, but
// BumpInstanceTailCount with a negative delta must behave the
// same so future call sites don't accidentally underflow.)
func TestMemStore_BumpInstanceTailCount_NegativeDeltaFloorsAtZero(t *testing.T) {
	ctx := context.Background()
	m := newMemStoreForTest()
	ins := seedTailCountInstanceRow(t, m)

	post, err := m.BumpInstanceTailCount(ctx, ins.ID, -5)
	if err != nil {
		t.Fatalf("bump -5: %v", err)
	}
	if post != 0 {
		t.Errorf("BumpInstanceTailCount(-5) on 0 = %d, want 0 (floor)", post)
	}
}

// TestMemStore_BumpDecrement_MissingRowReturnsErrNotFound pins
// the missing-row contract — both methods return ErrNotFound on
// unknown ids, matching the convention of
// SetInstanceFrameworkReadyAt / UpdateInstanceState. The vmmd
// receipt path translates this to a Debug drop.
func TestMemStore_BumpDecrement_MissingRowReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	m := newMemStoreForTest()
	const missing = "00000000-0000-0000-0000-deadbeefdead"

	if _, err := m.BumpInstanceTailCount(ctx, missing, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("BumpInstanceTailCount on missing = %v, want ErrNotFound", err)
	}
	if err := m.DecrementInstanceTailCount(ctx, missing, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("DecrementInstanceTailCount on missing = %v, want ErrNotFound", err)
	}
}
