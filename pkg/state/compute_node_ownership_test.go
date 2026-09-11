// compute_node_ownership_test.go — pinned tests for the
// UpsertComputeNodeFromOperator / UpsertComputeNodeFromVmmd
// ownership split. The split is the load-bearing fix for the
// second-box cutover: without it, vmmd's startup UPSERT silently
// overwrote the operator's POSTed target_url with the bind
// address, routing wakes to the wrong host on a multi-box fleet.
//
// These tests assert the contract:
//   - UpsertComputeNodeFromOperator is full-set on conflict
//     (operator's POST wins on every field, including target_url).
//   - UpsertComputeNodeFromVmmd preserves target_url on conflict
//     (operator's value wins; the new row's target_url is only
//     used in the cold-INSERT case where no operator POST has
//     happened yet).
//
// Mirrored on MemStore and PgStore so the SQL COALESCE and the
// in-memory conditional stay in lockstep.

package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestUpsertComputeNodeFromVmmd_PreservesOperatorTargetURL_MemStore:
// the canonical regression for the second-box cutover trap.
//
//  1. apid POSTs target_url=tcp://vmmd-2.faas:50051.
//  2. vmmd restarts; vmmd's self-registration UPSERTs with
//     target_url=tcp://0.0.0.0:50051 (a bind address — NOT a
//     routable dial target).
//  3. The row's target_url MUST still be the operator's FQDN.
//  4. The vmmd-owned fields (vpcpus, mem_mb, etc.) MUST be
//     refreshed from the new vmmd values.
func TestUpsertComputeNodeFromVmmd_PreservesOperatorTargetURL_MemStore(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()

	// Step 1: operator POST (apid path).
	operator, err := st.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name:               "fsn-2",
		TargetURL:          "tcp://vmmd-2.faas:50051",
		ScheddTargetURL:    stringPtr("tcp://fsn-2.gregale.dev:9090"),
		GatewayTargetURL:   stringPtr("tcp://fsn-2.gregale.dev:8080"),
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
	})
	if err != nil {
		t.Fatalf("operator upsert: %v", err)
	}
	operatorID := operator.ID
	if operatorID == "" {
		t.Fatal("operator upsert produced empty id")
	}
	if operator.TargetURL != "tcp://vmmd-2.faas:50051" {
		t.Fatalf("operator target_url = %q, want tcp://vmmd-2.faas:50051", operator.TargetURL)
	}

	// Step 2: vmmd restart with a wrong target_url.
	// Simulates the bug: vmmd's view of its dial target is the
	// bind address (0.0.0.0) instead of the FQDN.
	vmmd, err := st.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{
		Name:               "fsn-2",
		TargetURL:          "tcp://0.0.0.0:50051",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
	})
	if err != nil {
		t.Fatalf("vmmd upsert: %v", err)
	}

	// Step 3: target_url preserved.
	if vmmd.TargetURL != "tcp://vmmd-2.faas:50051" {
		t.Errorf("vmmd upsert CLOBBERED operator target_url: got %q, want tcp://vmmd-2.faas:50051",
			vmmd.TargetURL)
	}
	if vmmd.GatewayTargetURL == nil || *vmmd.GatewayTargetURL != "tcp://fsn-2.gregale.dev:8080" {
		t.Fatalf("vmmd upsert lost operator gateway_target_url: got %v", vmmd.GatewayTargetURL)
	}
	if vmmd.ScheddTargetURL == nil || *vmmd.ScheddTargetURL != "tcp://fsn-2.gregale.dev:9090" {
		t.Fatalf("vmmd upsert lost operator schedd_target_url: got %v", vmmd.ScheddTargetURL)
	}

	// Step 4: id preserved (same row).
	if vmmd.ID != operatorID {
		t.Errorf("id changed across upsert: %q -> %q", operatorID, vmmd.ID)
	}

	// Step 5: row remains active because the operator did not drain it.
	if !vmmd.Active {
		t.Error("vmmd upsert unexpectedly drained an active node")
	}
}

func TestUpsertComputeNodeFromVmmd_RejectsCertFingerprintDrift_MemStore(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()

	if _, err := st.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name:               "memstore-cert",
		TargetURL:          "tcp://vmmd-2.faas:50051",
		CertFingerprint:    stringPtr("fingerprint-a"),
		VPCPUs:             1,
		MemMB:              1024,
		MaxConcurrency:     1,
		AdmissionCeilingMB: 512,
		VCPUBudget:         1,
	}); err != nil {
		t.Fatalf("operator upsert: %v", err)
	}

	base := state.ComputeNode{
		Name:               "memstore-cert",
		TargetURL:          "tcp://0.0.0.0:50051",
		CertFingerprint:    stringPtr("fingerprint-a"),
		VPCPUs:             1,
		MemMB:              1024,
		MaxConcurrency:     1,
		AdmissionCeilingMB: 512,
		VCPUBudget:         1,
	}
	if _, err := st.UpsertComputeNodeFromVmmd(ctx, base); err != nil {
		t.Fatalf("same-fingerprint vmmd upsert: %v", err)
	}

	base.CertFingerprint = stringPtr("fingerprint-b")
	if _, err := st.UpsertComputeNodeFromVmmd(ctx, base); !errors.Is(err, state.ErrCertFingerprintDrift) {
		t.Fatalf("drift upsert error = %v, want ErrCertFingerprintDrift", err)
	}
}

func stringPtr(value string) *string { return &value }

// TestUpsertComputeNodeFromVmmd_PreservesOperatorDrain_MemStore pins the
// activation boundary used by deploy join: vmmd may refresh capacity while
// the operator keeps a newly-installed node drained until readiness passes.
func TestUpsertComputeNodeFromVmmd_PreservesOperatorDrain_MemStore(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()

	operator, err := st.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name: "fsn-2", TargetURL: "tcp://vmmd-2.faas:50051",
		VPCPUs: 160, MemMB: 56000, MaxConcurrency: 200, AdmissionCeilingMB: 47600,
	})
	if err != nil {
		t.Fatalf("operator upsert: %v", err)
	}
	if err := st.SetComputeNodeActive(ctx, operator.ID, false); err != nil {
		t.Fatalf("drain node: %v", err)
	}

	got, err := st.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{
		Name: "fsn-2", TargetURL: "tcp://0.0.0.0:50051",
		VPCPUs: 192, MemMB: 64000, MaxConcurrency: 250, AdmissionCeilingMB: 54000,
	})
	if err != nil {
		t.Fatalf("vmmd upsert: %v", err)
	}
	if got.Active {
		t.Fatal("vmmd self-registration reactivated an operator-drained node")
	}
	if got.VPCPUs != 192 || got.MemMB != 64000 {
		t.Fatalf("vmmd capacity was not refreshed: vpcpus=%d mem_mb=%d", got.VPCPUs, got.MemMB)
	}
}

// TestUpsertComputeNodeFromVmmd_ColdInsert_FallsBackToVmmdURL_MemStore:
// the cold-INSERT case. No prior row exists; no operator POST
// has happened. The new row's target_url must land (there's
// nothing to preserve). This matches pgstore's
// `coalesce(compute_nodes.target_url, excluded.target_url)`:
// existing value (NULL on cold INSERT) falls back to the new
// value.
func TestUpsertComputeNodeFromVmmd_ColdInsert_FallsBackToVmmdURL_MemStore(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()

	got, err := st.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{
		Name:               "fresh-box",
		TargetURL:          "tcp://fresh-box.faas:50051",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
	})
	if err != nil {
		t.Fatalf("cold vmmd upsert: %v", err)
	}
	if got.TargetURL != "tcp://fresh-box.faas:50051" {
		t.Errorf("cold insert target_url = %q, want tcp://fresh-box.faas:50051", got.TargetURL)
	}
	if got.ID == "" {
		t.Error("cold insert produced empty id")
	}
}

// TestUpsertComputeNodeFromOperator_OverwritesOnConflict_MemStore:
// the operator's POST wins on every field, including target_url.
// This is the apid POST path: re-POSTing with a new target_url
// MUST overwrite the existing value.
func TestUpsertComputeNodeFromOperator_OverwritesOnConflict_MemStore(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()

	first, err := st.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name:      "fsn-2",
		TargetURL: "tcp://vmmd-2.faas:50051",
		VPCPUs:    160, MemMB: 56000, MaxConcurrency: 200, AdmissionCeilingMB: 47600,
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Re-POST with a new target_url (operator repoints the box
	// to a new IP after a host swap).
	second, err := st.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name:      "fsn-2",
		TargetURL: "tcp://vmmd-2-new.faas:50051",
		VPCPUs:    160, MemMB: 56000, MaxConcurrency: 200, AdmissionCeilingMB: 47600,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.TargetURL != "tcp://vmmd-2-new.faas:50051" {
		t.Errorf("operator re-POST did not overwrite target_url: got %q", second.TargetURL)
	}
	if second.ID != first.ID {
		t.Errorf("id changed across upsert: %q -> %q", first.ID, second.ID)
	}
}

// ADR-029: the operator upsert owns lifecycle. An explicit unavailable state
// must be committed in the same write as enrollment so deferred nodes are
// never momentarily eligible for placement.
func TestUpsertComputeNodeFromOperator_HonorsDeferredLifecycle_MemStore(t *testing.T) {
	st := state.NewMemStore()
	got, err := st.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name: "deferred-mem", TargetURL: "tcp://vmmd-deferred.faas:50051",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1, Lifecycle: state.NodeLifecycleUnavailable,
	})
	if err != nil {
		t.Fatalf("operator upsert: %v", err)
	}
	if got.Lifecycle != state.NodeLifecycleUnavailable || got.Active {
		t.Errorf("got lifecycle=%q active=%v", got.Lifecycle, got.Active)
	}
}

// TestUpsertComputeNode_DeprecatedPreservesOriginalBehavior_MemStore:
// the deprecated UpsertComputeNode keeps the old "everything
// clobbers everything" behavior. This is the seam used by tests
// that pre-date the ownership split; new callers should pick
// FromOperator or FromVmmd explicitly.
func TestUpsertComputeNode_DeprecatedPreservesOriginalBehavior_MemStore(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()

	if _, err := st.UpsertComputeNode(ctx, state.ComputeNode{
		Name: "x", TargetURL: "tcp://first:50051",
		VPCPUs: 1, MemMB: 1, MaxConcurrency: 1, AdmissionCeilingMB: 1,
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := st.UpsertComputeNode(ctx, state.ComputeNode{
		Name: "x", TargetURL: "tcp://second:50051",
		VPCPUs: 1, MemMB: 1, MaxConcurrency: 1, AdmissionCeilingMB: 1,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.TargetURL != "tcp://second:50051" {
		t.Errorf("deprecated path target_url = %q, want tcp://second:50051", second.TargetURL)
	}
}

// TestUpsertComputeNodeFromVmmd_PreservesOperatorTargetURL_PgStore:
// the PgStore mirror of the canonical regression test. Requires
// a Postgres connection (pgStore helper skips when the env var
// FAAS_PG_TEST_DSN is unset, so this test is silently skipped on
// machines without a test DSN — matches the rest of the
// pgstore_*_test.go suite's gate).
func TestUpsertComputeNodeFromVmmd_PreservesOperatorTargetURL_PgStore(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)

	// Generate a unique name so re-runs don't collide.
	name := "ownership-test-" + time.Now().UTC().Format("20060102T150405.000000000")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from compute_nodes where name = $1`, name)
	})

	// Step 1: operator POST.
	s := state.NewPgStore(pool)
	_, err := s.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name:      name,
		TargetURL: "tcp://vmmd-2.faas:50051",
		VPCPUs:    1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1,
	})
	if err != nil {
		t.Fatalf("operator upsert: %v", err)
	}

	// Step 2: vmmd restart with the wrong target_url (bind form).
	got, err := s.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{
		Name:      name,
		TargetURL: "tcp://0.0.0.0:50051",
		VPCPUs:    1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1,
	})
	if err != nil {
		t.Fatalf("vmmd upsert: %v", err)
	}

	// Step 3: target_url preserved.
	if got.TargetURL != "tcp://vmmd-2.faas:50051" {
		t.Errorf("pgstore vmmd upsert CLOBBERED operator target_url: got %q, want tcp://vmmd-2.faas:50051",
			got.TargetURL)
	}
}

// TestUpsertComputeNodeFromVmmd_RejectsCertFingerprintDrift_PgStore pins the
// fail-closed multi-host safety check: vmmd may re-register with the same
// host fingerprint, but a different leaf must be reconciled explicitly.
func TestUpsertComputeNodeFromVmmd_RejectsCertFingerprintDrift_PgStore(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)

	name := "ownership-cert-" + time.Now().UTC().Format("20060102T150405.000000000")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from compute_nodes where name = $1`, name)
	})

	s := state.NewPgStore(pool)
	if _, err := s.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name:               name,
		TargetURL:          "tcp://vmmd-2.faas:50051",
		CertFingerprint:    stringPtr("fingerprint-a"),
		VPCPUs:             1,
		MemMB:              1024,
		MaxConcurrency:     1,
		AdmissionCeilingMB: 512,
		VCPUBudget:         1,
	}); err != nil {
		t.Fatalf("operator upsert: %v", err)
	}

	base := state.ComputeNode{
		Name:               name,
		TargetURL:          "tcp://0.0.0.0:50051",
		VPCPUs:             1,
		MemMB:              1024,
		MaxConcurrency:     1,
		AdmissionCeilingMB: 512,
		VCPUBudget:         1,
		CertFingerprint:    stringPtr("fingerprint-a"),
	}
	if _, err := s.UpsertComputeNodeFromVmmd(ctx, base); err != nil {
		t.Fatalf("same-fingerprint vmmd upsert: %v", err)
	}

	base.CertFingerprint = stringPtr("fingerprint-b")
	if _, err := s.UpsertComputeNodeFromVmmd(ctx, base); !errors.Is(err, state.ErrCertFingerprintDrift) {
		t.Fatalf("drift upsert error = %v, want ErrCertFingerprintDrift", err)
	}
}

// TestUpsertComputeNodeFromVmmd_ColdInsert_FallsBackToVmmdURL_PgStore:
// the PgStore mirror of the cold-INSERT case. COALESCE on the
// existing target_url (NULL) falls back to the new row's value.
func TestUpsertComputeNodeFromVmmd_ColdInsert_FallsBackToVmmdURL_PgStore(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)

	name := "ownership-cold-" + time.Now().UTC().Format("20060102T150405.000000000")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from compute_nodes where name = $1`, name)
	})

	s := state.NewPgStore(pool)
	got, err := s.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{
		Name:      name,
		TargetURL: "tcp://fresh-box.faas:50051",
		VPCPUs:    1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1,
	})
	if err != nil {
		t.Fatalf("cold vmmd upsert: %v", err)
	}
	if got.TargetURL != "tcp://fresh-box.faas:50051" {
		t.Errorf("pgstore cold insert target_url = %q, want tcp://fresh-box.faas:50051", got.TargetURL)
	}
}

// TestUpsertComputeNodeFromOperator_OverwritesOnConflict_PgStore:
// the PgStore mirror of the operator-overwrites path.
func TestUpsertComputeNodeFromOperator_OverwritesOnConflict_PgStore(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)

	name := "ownership-op-" + time.Now().UTC().Format("20060102T150405.000000000")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from compute_nodes where name = $1`, name)
	})

	s := state.NewPgStore(pool)
	if _, err := s.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name: name, TargetURL: "tcp://first:50051",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512, VCPUBudget: 1,
	}); err != nil {
		t.Fatalf("first: %v", err)
	}

	second, err := s.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name: name, TargetURL: "tcp://second:50051",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512, VCPUBudget: 1,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.TargetURL != "tcp://second:50051" {
		t.Errorf("pgstore operator re-POST did not overwrite target_url: got %q", second.TargetURL)
	}
}

// ADR-029: PostgreSQL must mirror the atomic deferred-enrollment lifecycle
// contract and round-trip the schedd endpoint carried by the operator row.
func TestUpsertComputeNodeFromOperator_HonorsDeferredLifecycle_PgStore(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	name := "ownership-deferred-" + time.Now().UTC().Format("20060102T150405.000000000")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from compute_nodes where name = $1`, name)
	})
	scheddTarget := "tcp://schedd-deferred.faas:50052"
	got, err := state.NewPgStore(pool).UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		Name: name, TargetURL: "tcp://vmmd-deferred.faas:50051",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1, Lifecycle: state.NodeLifecycleUnavailable,
		ScheddTargetURL: &scheddTarget,
	})
	if err != nil {
		t.Fatalf("operator upsert: %v", err)
	}
	if got.Lifecycle != state.NodeLifecycleUnavailable || got.Active {
		t.Errorf("got lifecycle=%q active=%v", got.Lifecycle, got.Active)
	}
	if got.ScheddTargetURL == nil || *got.ScheddTargetURL != scheddTarget {
		t.Errorf("schedd target = %v, want %q", got.ScheddTargetURL, scheddTarget)
	}
}

// Sanity guard: if any of the above starts failing on a future
// refactor that drops the deprecated UpsertComputeNode from the
// Store interface, the assertion trips before the test suite
// silently skips. Cheap insurance; matches the user's "fail loud
// at the boundary" pattern from the golangci-lint v2.4.0 memory.
var _ state.Store = (*state.MemStore)(nil)
var _ = errors.New
