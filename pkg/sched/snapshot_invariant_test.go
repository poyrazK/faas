package sched_test

// Property test scaffolding for spec invariant §6.2-3 ("An app always
// has a live snapshot OR a cold-bootable rootfs — never neither"),
// scoped to parked instances. PR scale-out readiness #4: this file is
// the scaffolding that lets a future production scanner enforce the
// invariant. The diagnostic helper takes explicit Instance /
// Deployment / Snapshot fixtures so it can be unit-tested without
// spinning up MemStore or Postgres.
//
// Why a pure helper instead of iterating state.Store.ListAllInstances:
// both MemStore and PgStore implementations of ListAllInstances
// intentionally exclude StateParked (memstore.go:2730-2746,
// pgstore.go:3579-3596) — the conntrack-reader contract is scoped to
// live instances only. A test that filtered for parked rows via
// ListAllInstances would inspect zero parked instances and pass
// vacuously. The helper therefore operates on explicit fixtures
// passed in by the table-driven test.
//
// Companion: pkg/sched/invariants_property_test.go (in-process
// NewMemStore fixtures, §6.2-1); pkg/sched/disk_drift.go (read-only
// file-system side of the same invariant).

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// snapshotExistenceViolations returns one human-readable string per
// invariant §6.2-3 breach across the parked instances in the input
// set. Empty slice means every parked instance has at least one of
// (snapshot row with non-empty StorageKey, cold-bootable rootfs).
//
// The helper returns strings instead of failing inside t.Errorf so the
// table-driven driver can assert the exact violation count for
// negative cases without weakening the negative assertions. This
// shape mirrors pkg/fcvm/snapshot.go:Usable returning a bool — the
// helper here aggregates violations and the caller decides what
// shape the assertion takes.
//
// Definitions (kept in lockstep with pkg/fcvm/snapshot.go:Usable
// at the FCVM seam and pkg/sched/disk_drift.go at the file-system
// seam):
//
//   - "snapshot exists" = a non-stale Snapshot row whose StorageKey
//     is non-empty. The state-layer Snapshot struct carries
//     StorageKey but not VMStateStorageKey/VMStatePath (those are
//     FCVM-level concerns); StorageKey == "" is the sentinel for
//     "no mem blob" at this layer, equivalent to the FCVM check.
//
//   - "cold-bootable rootfs" = Deployment.RootfsKey != "" OR
//     Deployment.RootfsPath != "". RootfsKey is the canonical
//     StorageBackend key for current rows; RootfsPath is the
//     legacy / pre-migration #116 carrier that some rows still
//     carry (see pkg/state/types.go:325-340).
func snapshotExistenceViolations(
	instances []state.Instance,
	deployments map[string]state.Deployment,
	snapshots map[string]state.Snapshot,
) []string {
	var violations []string
	for _, ins := range instances {
		// The invariant is scoped to parked instances. A RUNNING
		// instance with neither snapshot nor rootfs is fine —
		// Engine.Wake just produced a fresh row, no claim about
		// durability applies yet.
		if ins.State != string(state.StateParked) {
			continue
		}
		dep, ok := deployments[ins.DeploymentID]
		if !ok {
			violations = append(violations,
				"instance "+ins.ID+" parked without deployment row")
			continue
		}
		snap, hasSnap := snapshots[ins.DeploymentID]
		hasUsableSnap := hasSnap && !snap.Stale && snap.StorageKey != ""
		hasRootfs := dep.RootfsKey != "" || dep.RootfsPath != ""
		if hasUsableSnap || hasRootfs {
			continue
		}
		// Diagnostic message names the deployment id (not instance
		// id) so the violation localises to the offending deployment
		// — multiple parked instances of the same app fail together,
		// which matches the invariant's app-scoped phrasing.
		violations = append(violations,
			"deployment "+dep.ID+" parked with neither snapshot nor cold-bootable rootfs")
	}
	return violations
}

// TestSnapshotExistenceInvariant_TableDriven exercises the helper
// across the cases the plan enumerates. Subtest names match the
// pkg/sched/heartbeat_test.go:282 space-separated convention. Each
// row records both the fixture shape and the expected violation
// count, so a future contributor who breaks a positive case surfaces
// immediately.
//
// We do NOT add a parallel "live invariant scanner" path — that
// would require a new state.Store query (ListInstancesForApp +
// LatestSnapshot per app, scoped to parked), which exceeds the test
// scaffolding scope of PR scale-out readiness #4.
//
// Subtests fall in two groups:
//
//   - 7 §6.2-3 cases (parked instances + their durability): the
//     invariant under test.
//   - 3 diagnostic cases (orphan parked row, running instance, nil
//     snapshots map): the helper's stability under odd inputs.
//     Distinguishing the two groups is purely reader scaffolding;
//     the violation-counting assertion applies uniformly.
func TestSnapshotExistenceInvariant_TableDriven(t *testing.T) {
	type fixture struct {
		name        string
		instances   []state.Instance
		deployments map[string]state.Deployment
		snapshots   map[string]state.Snapshot
		wantCount   int
		// wantContains: when wantCount > 0, the violation messages
		// must contain this substring (best-effort locality check).
		// Empty means no substring check.
		wantContains string
	}
	cases := []fixture{
		{
			name: "parked with snapshot row passes",
			instances: []state.Instance{
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-1"},
			},
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1"},
			},
			snapshots: map[string]state.Snapshot{
				"d-1": {ID: "s-1", DeploymentID: "d-1", StorageKey: "snap/d-1/mem"},
			},
			wantCount: 0,
		},
		{
			name: "parked with snapshot row missing mem fails",
			instances: []state.Instance{
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-1"},
			},
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1"},
			},
			snapshots: map[string]state.Snapshot{
				// StorageKey == "" is the sentinel for "no mem
				// blob". This is the row shape that pkg/fcvm/
				// snapshot.go:Usable rejects at the FCVM seam.
				"d-1": {ID: "s-1", DeploymentID: "d-1", StorageKey: ""},
			},
			wantCount:    1,
			wantContains: "d-1",
		},
		{
			name: "parked with stale snapshot row fails",
			instances: []state.Instance{
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-1"},
			},
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1"},
			},
			snapshots: map[string]state.Snapshot{
				// Stale=true means imaged marked this row on FC
				// upgrade (ADR-005). The state-layer query for
				// "latest usable snapshot" must filter Stale.
				"d-1": {ID: "s-1", DeploymentID: "d-1", StorageKey: "snap/d-1/mem", Stale: true},
			},
			wantCount:    1,
			wantContains: "d-1",
		},
		{
			name: "parked with cold-bootable rootfs and no snapshot passes",
			instances: []state.Instance{
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-1"},
			},
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1", RootfsKey: "rootfs/d-1.ext4"},
			},
			snapshots: map[string]state.Snapshot{},
			wantCount: 0,
		},
		{
			name: "parked with legacy rootfs path passes",
			instances: []state.Instance{
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-1"},
			},
			// RootfsKey == "" but RootfsPath != "" — pre-#116
			// legacy row that imaged has not yet re-stamped
			// (pkg/state/types.go:336-339 documents this).
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1", RootfsPath: "/srv/fc/rootfs/d-1.ext4"},
			},
			snapshots: map[string]state.Snapshot{},
			wantCount: 0,
		},
		{
			name: "parked with neither snapshot nor rootfs fails",
			instances: []state.Instance{
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-1"},
			},
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1"},
			},
			snapshots:    map[string]state.Snapshot{},
			wantCount:    1,
			wantContains: "d-1",
		},
		{
			name: "running instance is outside parked invariant",
			instances: []state.Instance{
				// RUNNING with neither snapshot nor rootfs is
				// fine: Engine.Wake just produced a fresh row,
				// no durability claim yet.
				{ID: "i-1", State: string(state.StateRunning), DeploymentID: "d-1"},
			},
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1"},
			},
			snapshots: map[string]state.Snapshot{},
			wantCount: 0,
		},
		{
			name: "multiple parked, one violates",
			instances: []state.Instance{
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-1"},
				{ID: "i-2", State: string(state.StateParked), DeploymentID: "d-2"},
				{ID: "i-3", State: string(state.StateParked), DeploymentID: "d-3"},
			},
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1"},
				"d-2": {ID: "d-2", RootfsKey: "rootfs/d-2.ext4"},
				"d-3": {ID: "d-3"},
			},
			snapshots: map[string]state.Snapshot{
				"d-1": {ID: "s-1", DeploymentID: "d-1", StorageKey: "snap/d-1/mem"},
			},
			// Only d-3 violates. d-1 has a snapshot, d-2 has a
			// rootfs; both pass.
			wantCount:    1,
			wantContains: "d-3",
		},
		{
			name: "orphan parked row surfaces diagnostic (not §6.2-3)",
			instances: []state.Instance{
				// Orphan parked row: no matching deployment in
				// the lookup. The helper surfaces this as a
				// distinct violation so a future scanner can
				// alarm on "parked without deployment" without
				// conflating it with the §6.2-3 case. This
				// subtest is diagnostic scaffolding, not a
				// pin on the invariant itself — its name
				// reflects that distinction.
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-missing"},
			},
			deployments:  map[string]state.Deployment{},
			snapshots:    map[string]state.Snapshot{},
			wantCount:    1,
			wantContains: "i-1",
		},
		{
			name: "nil snapshots map is no different from empty",
			// A future caller passing nil instead of an empty
			// map shouldn't get a nil-pointer panic and
			// shouldn't change the answer. Go map indexing on
			// a nil map returns the zero value, so the helper
			// should evaluate identically to the empty-map case.
			instances: []state.Instance{
				{ID: "i-1", State: string(state.StateParked), DeploymentID: "d-1"},
			},
			deployments: map[string]state.Deployment{
				"d-1": {ID: "d-1"},
			},
			snapshots:    nil,
			wantCount:    1,
			wantContains: "d-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := snapshotExistenceViolations(tc.instances, tc.deployments, tc.snapshots)
			if len(got) != tc.wantCount {
				t.Errorf("violations = %d (%v), want %d", len(got), got, tc.wantCount)
				return
			}
			if tc.wantContains != "" && len(got) > 0 {
				found := false
				for _, v := range got {
					if strings.Contains(v, tc.wantContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("violations %v should contain %q", got, tc.wantContains)
				}
			}
		})
	}
}
