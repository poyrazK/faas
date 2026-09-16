package wire

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGNodeLoaderKeepsDrainingSourcesAuthenticated(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)

	create := func(lifecycle state.NodeLifecycle) state.ComputeNode {
		t.Helper()
		node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{
			Name:               "verifier-" + uuid.NewString(),
			TargetURL:          "unix:///run/faas/vmmd.sock",
			Lifecycle:          lifecycle,
			MemMB:              8192,
			MaxConcurrency:     16,
			AdmissionCeilingMB: 4096,
			VPCPUs:             4,
			VCPUBudget:         160,
		})
		if err != nil {
			t.Fatalf("CreateComputeNode(%s): %v", lifecycle, err)
		}
		return node
	}

	active := create(state.NodeLifecycleActive)
	draining := create(state.NodeLifecycleDraining)
	forceDraining := create(state.NodeLifecycleForceDraining)
	maintenance := create(state.NodeLifecycleMaintenance)
	retired := create(state.NodeLifecycleRetired)

	rows, err := NewPGNodeLoader(pool).LoadNodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(rows))
	for _, row := range rows {
		got[row.CN] = true
	}
	for _, node := range []state.ComputeNode{active, draining, forceDraining} {
		if !got[node.Name] {
			t.Errorf("lifecycle %s node %q missing from verifier", node.Lifecycle, node.Name)
		}
	}
	for _, node := range []state.ComputeNode{maintenance, retired} {
		if got[node.Name] {
			t.Errorf("lifecycle %s node %q unexpectedly authenticated", node.Lifecycle, node.Name)
		}
	}
}
