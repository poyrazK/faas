// Tests for pkg/state/deployment_sidecar_rams.go (issue #463 /
// ADR-070 / PR-C sidecar-aware billing broker).
//
// Two layers of coverage:
//
//   - Whitebox unit tests on MemStore (no DB) — the in-memory twin
//     mirrors the JSONB decode path so we can exercise nil / empty /
//     malformed / maximum-cap shapes without spinning up pgtest.
//
//   - Blackbox tests on PgStore (pgtest) — pins the SQL surface
//     against the live schema. Migration 00118 owns the column
//     CHECK, but this test pins the SELECT shape so a future
//     schema change (normalized sidecar table) is caught here.

package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestDeploymentSidecarRAMs_MemStore_NoSidecars pins the legacy
// no-sidecar shape: a deployment with no `sidecars` jsonb entry
// returns nil — which BillableRAMMBWithSidecars collapses to the
// single-arg form, matching PR-B's admission math.
func TestDeploymentSidecarRAMs_MemStore_NoSidecars(t *testing.T) {
	s := state.NewMemStore()
	ctx := context.Background()
	app, err := s.CreateApp(ctx, state.App{AccountID: "acct1", Slug: "no-sidecars-" + t.Name(), RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	got, err := s.DeploymentSidecarRAMs(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentSidecarRAMs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("DeploymentSidecarRAMs (no sidecars) = %v; want nil", got)
	}
}

// TestDeploymentSidecarRAMs_MemStore_MaxHelpers pins the maximum helper
// resource slice; every helper's RAM must reach admission accounting.
func TestDeploymentSidecarRAMs_MemStore_MaxHelpers(t *testing.T) {
	s := state.NewMemStore()
	ctx := context.Background()
	app, err := s.CreateApp(ctx, state.App{AccountID: "acct1", Slug: "five-companions-" + t.Name(), RAMMB: 512, Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:    app.ID,
		Status:   state.DeployLive,
		Sidecars: []byte(`[{"ram_mb":64},{"ram_mb":32},{"ram_mb":48},{"ram_mb":16},{"ram_mb":64}]`),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	got, err := s.DeploymentSidecarRAMs(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentSidecarRAMs: %v", err)
	}
	want := []int{64, 32, 48, 16, 64}
	if len(got) != len(want) {
		t.Fatalf("DeploymentSidecarRAMs = %v; want %v (len mismatch)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DeploymentSidecarRAMs[%d] = %d; want %d", i, got[i], want[i])
		}
	}
}

// TestDeploymentSidecarRAMs_MemStore_ZeroRamMB pins the "inherit
// plan RAM" sentinel: ram_mb:0 is preserved verbatim so the cgroup
// scope and the billing shutter see the same value.
func TestDeploymentSidecarRAMs_MemStore_ZeroRamMB(t *testing.T) {
	s := state.NewMemStore()
	ctx := context.Background()
	app, err := s.CreateApp(ctx, state.App{AccountID: "acct1", Slug: "zero-ram-" + t.Name(), RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:    app.ID,
		Status:   state.DeployLive,
		Sidecars: []byte(`[{"ram_mb":0}]`),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	got, err := s.DeploymentSidecarRAMs(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentSidecarRAMs: %v", err)
	}
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("DeploymentSidecarRAMs (zero ram_mb) = %v; want [0]", got)
	}
}

// TestDeploymentSidecarRAMs_MemStore_Malformed pins the fail-closed
// shape: a corrupted jsonb entry returns an error and nil, so
// schedd's Request carries SidecarMBs=nil and the admission
// reverts to the legacy no-sidecar form (the over-admission
// would be worse than the under-admission on a transient).
func TestDeploymentSidecarRAMs_MemStore_Malformed(t *testing.T) {
	s := state.NewMemStore()
	ctx := context.Background()
	app, err := s.CreateApp(ctx, state.App{AccountID: "acct1", Slug: "malformed-" + t.Name(), RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:    app.ID,
		Status:   state.DeployLive,
		Sidecars: []byte(`not-json`),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	got, err := s.DeploymentSidecarRAMs(ctx, dep.ID)
	if err == nil {
		t.Fatalf("DeploymentSidecarRAMs (malformed) err = nil; want non-nil")
	}
	if !strings.Contains(err.Error(), "decode sidecars") {
		t.Errorf("err = %q; want substring %q", err.Error(), "decode sidecars")
	}
	if got != nil {
		t.Errorf("DeploymentSidecarRAMs (malformed) = %v; want nil", got)
	}
}

// TestDeploymentSidecarRAMs_MemStore_EmptyDeploymentID pins the
// argument-validation gate — same shape as the rest of the state
// package's primary-key lookups.
func TestDeploymentSidecarRAMs_MemStore_EmptyDeploymentID(t *testing.T) {
	s := state.NewMemStore()
	if _, err := s.DeploymentSidecarRAMs(context.Background(), ""); err == nil {
		t.Fatal("DeploymentSidecarRAMs(\"\") err = nil; want non-nil")
	}
}

// TestDeploymentSidecarRAMs_MemStore_UnknownDeployment pins the
// missing-row behavior: an unknown deployment_id returns
// (nil, nil) — the broker treats "no row found" the same as
// "row found but no sidecars". This matches schedd's expectation
// during the legacy single-box path where a deployment_id may
// not exist in the cache yet (cold-start of the engine).
func TestDeploymentSidecarRAMs_MemStore_UnknownDeployment(t *testing.T) {
	s := state.NewMemStore()
	got, err := s.DeploymentSidecarRAMs(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("DeploymentSidecarRAMs (unknown): %v", err)
	}
	if got != nil {
		t.Errorf("DeploymentSidecarRAMs (unknown) = %v; want nil", got)
	}
}
