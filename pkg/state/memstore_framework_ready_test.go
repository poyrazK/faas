package state

// FrameworkReadyAt MemStore tests (issue #470 / PR #470-FU-B).
// White-box package (`package state`) so the tests can call the
// unexported helpers (newMemStoreForTest, the seed IDs) directly —
// same pattern as memstore_warm_snapshot_test.go. The pgstore
// equivalents live in pgstore_warm_snapshot_test.go (//go:build
// !no_pg + pgtest.Open) and a new pgstore_framework_ready_test.go
// covering the same shape as this file.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// seedFrameworkReadyInstanceRow inserts a single instance row in the
// "running" state inside an app+deployment root. The IDs are fixed
// so a failure message can name them directly; tests don't depend on
// them. Mirrors seedTierAppAndDeployment's structure with its own
// app+deployment pair to avoid colliding with the warm-snapshot test
// fixtures.
func seedFrameworkReadyInstanceRow(t *testing.T, m *MemStore) Instance {
	t.Helper()
	ctx := context.Background()
	account, err := m.CreateAccount(ctx, "framework-ready@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateApp(ctx, App{
		ID: "00000000-0000-0000-0000-0000000000d0", AccountID: account.ID,
		Slug: "framework-ready-app", Type: AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
		Status: AppActive, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := m.CreateDeployment(ctx, Deployment{
		ID: "00000000-0000-0000-0000-0000000000e0", AppID: "00000000-0000-0000-0000-0000000000d0",
		ImageDigest: "sha256:frameworkready", Status: DeployLive, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	ins, err := m.CreateInstance(ctx, "00000000-0000-0000-0000-0000000000d0",
		"00000000-0000-0000-0000-0000000000e0", "running", 256, "local-0", "")
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	return ins
}

// TestMemStore_SetInstanceFrameworkReadyAt_StampsAndClears confirms the
// SetInstanceFrameworkReadyAt + ClearInstanceFrameworkReadyAt pair round-trip:
// fresh row has FrameworkReadyAt == nil; set stamps to a non-nil time;
// clear resets to nil. The MemStore mirror of pgstore behaviour.
func TestMemStore_SetInstanceFrameworkReadyAt_StampsAndClears(t *testing.T) {
	ctx := context.Background()
	m := newMemStoreForTest()
	ins := seedFrameworkReadyInstanceRow(t, m)

	// Fresh row has no framework-ready stamp.
	got, err := m.InstanceByID(ctx, ins.ID)
	if err != nil {
		t.Fatalf("read fresh instance: %v", err)
	}
	if got.FrameworkReadyAt != nil {
		t.Errorf("fresh instance FrameworkReadyAt = %v, want nil", got.FrameworkReadyAt)
	}

	// Set stamps the timestamp.
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	if err := m.SetInstanceFrameworkReadyAt(ctx, ins.ID, stamp); err != nil {
		t.Fatalf("SetInstanceFrameworkReadyAt: %v", err)
	}
	got, err = m.InstanceByID(ctx, ins.ID)
	if err != nil {
		t.Fatalf("read after set: %v", err)
	}
	if got.FrameworkReadyAt == nil {
		t.Fatalf("SetInstanceFrameworkReadyAt did not stamp; got nil")
	}
	if !got.FrameworkReadyAt.Equal(stamp) {
		t.Errorf("FrameworkReadyAt = %v, want %v", got.FrameworkReadyAt, stamp)
	}

	// Clear resets to nil.
	if err := m.ClearInstanceFrameworkReadyAt(ctx, ins.ID); err != nil {
		t.Fatalf("ClearInstanceFrameworkReadyAt: %v", err)
	}
	got, err = m.InstanceByID(ctx, ins.ID)
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if got.FrameworkReadyAt != nil {
		t.Errorf("post-clear FrameworkReadyAt = %v, want nil", got.FrameworkReadyAt)
	}
}

// TestMemStore_SetInstanceFrameworkReadyAt_MissingRowReturnsErrNotFound
// confirms the missing-row contract — SetInstanceFrameworkReadyAt on
// an unknown id returns ErrNotFound, matching the convention of
// UpdateInstanceState and UpdateInstanceStateToTerminal. The engine
// in PR #470-FU-A uses this to distinguish "instance already gone"
// from transient DB errors.
func TestMemStore_SetInstanceFrameworkReadyAt_MissingRowReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	m := newMemStoreForTest()
	err := m.SetInstanceFrameworkReadyAt(ctx, "00000000-0000-0000-0000-deadbeefdead", time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("SetInstanceFrameworkReadyAt on missing row = %v, want ErrNotFound", err)
	}
	err = m.ClearInstanceFrameworkReadyAt(ctx, "00000000-0000-0000-0000-deadbeefdead")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ClearInstanceFrameworkReadyAt on missing row = %v, want ErrNotFound", err)
	}
}

// TestMemStore_SetInstanceFrameworkReadyAt_ReStampsAfterClear is the
// round-trip the engine exercises on every warm-capture cycle:
// clear-then-set in two consecutive calls, each stamped with a
// different time. The latest stamp wins (the previous stamp is
// overwritten by the UPDATE call).
func TestMemStore_SetInstanceFrameworkReadyAt_ReStampsAfterClear(t *testing.T) {
	ctx := context.Background()
	m := newMemStoreForTest()
	ins := seedFrameworkReadyInstanceRow(t, m)

	first := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if err := m.SetInstanceFrameworkReadyAt(ctx, ins.ID, first); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if err := m.ClearInstanceFrameworkReadyAt(ctx, ins.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}

	second := time.Now().UTC().Truncate(time.Microsecond)
	if err := m.SetInstanceFrameworkReadyAt(ctx, ins.ID, second); err != nil {
		t.Fatalf("second set: %v", err)
	}
	got, err := m.InstanceByID(ctx, ins.ID)
	if err != nil {
		t.Fatalf("read after re-stamp: %v", err)
	}
	if got.FrameworkReadyAt == nil {
		t.Fatalf("re-set did not stamp; got nil")
	}
	if !got.FrameworkReadyAt.Equal(second) {
		t.Errorf("FrameworkReadyAt = %v, want %v (second stamp)", got.FrameworkReadyAt, second)
	}
	if got.FrameworkReadyAt.Equal(first) {
		t.Errorf("FrameworkReadyAt still on first stamp %v; expected second %v", first, second)
	}
}
