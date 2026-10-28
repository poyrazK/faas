// adr: 20261027220445 — the durable per-node reservation bounds the vCPU and
// CPU an admission books, as placement does, with the scheduler's quiet-pause
// discount bounded by the ADR-1151 cap.
package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type computeNodeFixture struct {
	s         state.Store
	node      state.ComputeNode
	app, dep  string
	resource  string
	usedValue func() int64
}

func newComputeNodeFixture(t *testing.T, s state.Store, resource string, vpcpus, vcpuBudget int) *computeNodeFixture {
	t.Helper()
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "compute-" + uuid.NewString(), Lifecycle: state.NodeLifecycleActive,
		TargetURL: "unix:///run/faas/vmmd.sock", VPCPUs: vpcpus, VCPUBudget: vcpuBudget,
		MemMB: 1 << 20, AdmissionCeilingMB: 1 << 19, MaxConcurrency: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &computeNodeFixture{s: s, node: node, resource: resource}
	f.app, f.dep = completionApplication(t, s)
	f.usedValue = func() int64 {
		t.Helper()
		var used map[string]int64
		var err error
		switch resource {
		case "vcpu":
			used, err = s.(state.ComputeNodeVCPUUsageBatcher).ComputeNodeUsedVCPUByNode(t.Context(), []string{node.ID})
		default:
			used, err = s.(state.ComputeNodeCPUUsageBatcher).ComputeNodeUsedCPUMillicoresByNode(t.Context(), []string{node.ID})
		}
		if err != nil {
			t.Fatal(err)
		}
		return used[node.ID]
	}
	return f
}

func (f *computeNodeFixture) admit(t *testing.T, c state.NodeCompute) error {
	t.Helper()
	ctx := state.WithNodeAdmission(t.Context(), state.NodeAdmission{NodeID: f.node.ID, Generation: f.node.LifecycleGeneration, Compute: c})
	_, err := f.s.CreateInstanceWithMode(ctx, f.app, f.dep, string(state.StateWaking), 128, f.node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	return err
}

// fill admits until the guard refuses, and requires the refusal to name the
// fixture's resource.
func (f *computeNodeFixture) fill(t *testing.T, c state.NodeCompute, step string) {
	t.Helper()
	for range 64 {
		err := f.admit(t, c)
		if err == nil {
			continue
		}
		if !errors.Is(err, state.ErrNodeCapacity) || !strings.Contains(err.Error(), "resource="+f.resource) {
			t.Fatalf("%s: admission = %v, want a %s capacity refusal", step, err, f.resource)
		}
		return
	}
	t.Fatalf("%s: the guard never refused", step)
}

func checkNodeComputeReservation(t *testing.T, s state.Store) {
	t.Helper()
	vcpus := int64(api.MustLimitsFor(api.PlanPro).VCPU)
	maxShare := int64(api.QuietPauseMaxVCPUOvercommitPercent)

	t.Run("vcpu", func(t *testing.T) {
		budget := 3*vcpus + 1
		f := newComputeNodeFixture(t, s, "vcpu", 64, int(budget))
		request := state.NodeCompute{VCPU: int(vcpus)}
		f.fill(t, request, "no discount")
		if used := f.usedValue(); used > budget || used+vcpus <= budget {
			t.Fatalf("node full at %d vCPU, want the most that fits %d", used, budget)
		}
		discounted := request
		discounted.QuietVCPU = int(vcpus)
		if err := f.admit(t, discounted); err != nil {
			t.Fatalf("admission beside a quiet-paused instance: %v", err)
		}
		discounted.QuietVCPU = 1 << 20
		f.fill(t, discounted, "oversized discount")
		if used, capVCPU := f.usedValue(), budget*maxShare/100; used > capVCPU || used+vcpus <= capVCPU {
			t.Fatalf("an oversized discount let the node reach %d vCPU, want the most that fits %d (%d%% of the budget)", used, capVCPU, maxShare)
		}
		if err := f.admit(t, state.NodeCompute{}); err != nil {
			t.Fatalf("an admission without compute is checked against RAM alone: %v", err)
		}
	})

	t.Run("cpu", func(t *testing.T) {
		budget := int64(1000 * api.CPUOvercommit)
		f := newComputeNodeFixture(t, s, "cpu", 1, 1<<20)
		request := state.NodeCompute{CPUMillicores: api.DefaultAppCPUMillicores}
		f.fill(t, request, "no discount")
		if used := f.usedValue(); used > budget || used+api.DefaultAppCPUMillicores <= budget {
			t.Fatalf("node full at %d millicores, want the most that fits %d", used, budget)
		}
		discounted := request
		discounted.QuietCPUMillicores = api.DefaultAppCPUMillicores
		if err := f.admit(t, discounted); err != nil {
			t.Fatalf("admission beside a quiet-paused instance: %v", err)
		}
	})
}

func TestNodeComputeReservationMemory(t *testing.T) {
	checkNodeComputeReservation(t, state.NewMemStore())
}
