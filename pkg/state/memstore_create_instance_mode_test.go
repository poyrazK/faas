// memstore_create_instance_mode_test.go — pin the MemStore
// CreateInstanceWithMode stamping for issue #72 / ADR-124 /
// ADR-125 PR-A3.
//
// The mode='mirror' stamp is load-bearing for two reasons:
//   1. meter/sampler (pkg/meter/sampler.go:386) and reaper
//      (pkg/sched/reaper.go:301) skip instances where mode='mirror'
//      so the customer is never billed for the mirror VM's
//      running seconds (PR-A1 / migration 00385).
//   2. The `mirror_invocation_results` ledger row cross-references
//      the admitting instance via InstanceID — getting mode='mirror'
//      wrong (defaulting to 'normal') would let the reaper park the
//      mirror VM mid-flight, killing the ledger row's reference.
//
// The MemStore pins the in-memory shape; PgStore coverage is in
// pkg/state/pgtest via a separate test that exercises the
// migrations/00385 CHECK constraint.

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

// TestMemStore_CreateInstanceWithMode_StampsMirror pins that a
// freshly created instance carries Instance.Mode='mirror' when
// mode='mirror' is passed (so the reaper/sampler skip path fires).
// The mode column is load-bearing for two reasons:
//  1. meter/sampler (pkg/meter/sampler.go:386) and reaper
//     (pkg/sched/reaper.go:301) skip instances where mode='mirror'
//     so the customer is never billed for the mirror VM's
//     running seconds.
//  2. The `mirror_invocation_results` ledger row cross-references
//     the admitting instance via InstanceID — getting mode='mirror'
//     wrong (defaulting to empty) would let the reaper park the
//     mirror VM mid-flight, killing the ledger row's reference.
//
// Legacy callers (CreateInstance, no mode arg) DO NOT default
// Mode='normal' on MemStore — PgStore relies on the SQL DEFAULT
// from migration 00385 to do that. This is a known divergence
// (gaps analysis 2026-07-23): MemStore is permissive, PgStore
// enforces the CHECK. The mirror hot path uses
// CreateInstanceWithMode exclusively so this test only pins that
// contract — the legacy path is exercised by PgStore and
// pgtest (PR-A3 follow-on / ADR-098).
func TestMemStore_CreateInstanceWithMode_StampsMirror(t *testing.T) {
	m := state.NewMemStoreForTest()
	ctx := t.Context()

	ins, err := m.CreateInstanceWithMode(ctx, "app-1", "dep-mirror", "RUNNING", 256, "node-A", "wake-1",
		string(state.InstanceModeMirror))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode(mirror): %v", err)
	}
	if ins.Mode != string(state.InstanceModeMirror) {
		t.Errorf("Mode = %q, want %q", ins.Mode, state.InstanceModeMirror)
	}
}

// TestMemStore_CreateInstanceWithMode_DefaultsEmptyToNormal pins the
// empty-mode fallback. A future patch that drops an empty mode
// straight through (rather than normalising) would silently
// trip the 00385 CHECK on PgStore and surface as a SQLSTATE 23514
// at runtime; this test makes the MemStore-side behaviour
// deterministic.
func TestMemStore_CreateInstanceWithMode_DefaultsEmptyToNormal(t *testing.T) {
	m := state.NewMemStoreForTest()
	ctx := t.Context()

	ins, err := m.CreateInstanceWithMode(ctx, "app-2", "dep-2", "RUNNING", 256, "node-A", "wake-3", "")
	if err != nil {
		t.Fatalf("CreateInstanceWithMode(empty): %v", err)
	}
	if ins.Mode != string(state.InstanceModeNormal) {
		t.Errorf("empty Mode = %q, want %q", ins.Mode, state.InstanceModeNormal)
	}
}
