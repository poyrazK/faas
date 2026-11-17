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
	return f.admitBooked(t, c, 0)
}

// admitBooked admits with a CPU booking stamped on the row
// (ADR-20261101160231 step 5b); zero books the app's CPU.
func (f *computeNodeFixture) admitBooked(t *testing.T, c state.NodeCompute, booked int) error {
	t.Helper()
	return f.admitBookings(t, c, booked, 0)
}

// admitBookings also stamps the guest vCPU count the instance boots with
// (ADR-20261109172421 step 5); zero books its size's.
func (f *computeNodeFixture) admitBookings(t *testing.T, c state.NodeCompute, booked, bookedVCPU int) error {
	t.Helper()
	ctx := state.WithNodeAdmission(t.Context(), state.NodeAdmission{NodeID: f.node.ID, Generation: f.node.LifecycleGeneration,
		Compute: c, BookedCPUMillicores: booked, BookedVCPU: bookedVCPU})
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
	vcpus := int64(api.SizeVCPUs(128)) // the fixture's instances are 128 MB, one vCPU each
	maxShare := int64(api.MeasuredCPUMaxOvercommitPercent)

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

	// ADR-20261101160231 step 5b: a stamped booking replaces the app's CPU
	// in the node's sum, and an unstamped row keeps the app's.
	t.Run("booked cpu", func(t *testing.T) {
		f := newComputeNodeFixture(t, s, "cpu", 64, 1<<20)
		before := f.usedValue()
		if err := f.admitBooked(t, state.NodeCompute{CPUMillicores: 250}, 250); err != nil {
			t.Fatal(err)
		}
		booked := f.usedValue()
		if booked-before != 250 {
			t.Fatalf("a row booked at 250 m added %d m to the node", booked-before)
		}
		if err := f.admit(t, state.NodeCompute{CPUMillicores: api.DefaultAppCPUMillicores}); err != nil {
			t.Fatal(err)
		}
		if got := f.usedValue() - booked; got != api.DefaultAppCPUMillicores {
			t.Fatalf("an unbooked row added %d m, want its app's %d", got, api.DefaultAppCPUMillicores)
		}
		if err := f.admitBooked(t, state.NodeCompute{CPUMillicores: 2000}, 2000); err != nil {
			t.Fatal(err)
		}
	})

	// ADR-20261109172421 step 5: a stamped guest vCPU count is the row's in
	// the node's vCPU sum, and an unstamped row counts its size's, one per GB
	// (step 6.4; the fixture's instances are 128 MB).
	t.Run("booked vcpu", func(t *testing.T) {
		f := newComputeNodeFixture(t, s, "vcpu", 64, 1<<20)
		before := f.usedValue()
		if err := f.admitBookings(t, state.NodeCompute{VCPU: 1}, 125, 1); err != nil {
			t.Fatal(err)
		}
		booked := f.usedValue()
		if booked-before != 1 {
			t.Fatalf("a row booked at 1 vCPU added %d vCPU to the node", booked-before)
		}
		size := api.SizeVCPUs(128)
		if err := f.admit(t, state.NodeCompute{VCPU: size}); err != nil {
			t.Fatal(err)
		}
		if got := f.usedValue() - booked; got != int64(size) {
			t.Fatalf("an unbooked row added %d vCPU, want its size's %d", got, size)
		}
		if err := f.admitBookings(t, state.NodeCompute{VCPU: 2}, 0, 2); err != nil {
			t.Fatalf("a vCPU count without a CPU booking: %v", err)
		}
		if got := f.usedValue() - booked - int64(size); got != 2 {
			t.Fatalf("a row booked at 2 vCPU without a CPU booking added %d vCPU", got)
		}
	})
}

func TestNodeComputeReservationMemory(t *testing.T) {
	checkNodeComputeReservation(t, state.NewMemStore())
}
