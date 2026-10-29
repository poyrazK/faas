// adr: 20261027172037 — a SNAPSHOTTING instance holds its RAM on the node for
// placement's sum and the durable reservation alike.
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkSnapshottingHoldsNodeRAM(t *testing.T, s state.Store) {
	t.Helper()
	const ceilingMB, ramMB = 1024, 248
	perInstance := int64(ramMB + api.PerVMOverheadMB)
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "snapshotting-" + uuid.NewString(), Lifecycle: state.NodeLifecycleActive,
		TargetURL: "unix:///run/faas/vmmd.sock", VPCPUs: 4, VCPUBudget: 160,
		MemMB: 8192, AdmissionCeilingMB: ceilingMB, MaxConcurrency: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, dep := completionApplication(t, s)
	create := func(st state.State) error {
		_, err := s.CreateInstanceWithMode(t.Context(), app, dep, string(st), ramMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
		return err
	}
	for range 3 {
		if err := create(state.StateSnapshotting); err != nil {
			t.Fatal(err)
		}
	}
	used, err := s.ComputeNodeUsedMB(t.Context(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 3*perInstance {
		t.Fatalf("node with three capturing instances books %d MB, want %d", used, 3*perInstance)
	}
	if err := create(state.StateWaking); err != nil {
		t.Fatalf("admission that fits beside the capturing instances: %v", err)
	}
	if err := create(state.StateWaking); !errors.Is(err, state.ErrNodeCapacity) {
		t.Fatalf("admission past the ceiling counting capturing instances = %v, want ErrNodeCapacity", err)
	}
}

func TestSnapshottingHoldsNodeRAMMemory(t *testing.T) {
	checkSnapshottingHoldsNodeRAM(t, state.NewMemStore())
}
