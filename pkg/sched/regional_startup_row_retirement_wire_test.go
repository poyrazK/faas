package sched_test

// adr: 788
import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Synthetic native evidence exercises authenticated transport and accounting;
// it does not execute native processes or establish lifecycle acceptance.
type startupRetirementWireVMM struct {
	*retirementWireVMM
	source runtimequota.LaunchSource
}

func (v *startupRetirementWireVMM) RegionalRuntimeHostEpoch() (runtimequota.HostEpoch, error) {
	return runtimequota.HostEpoch{NodeID: v.source.Permit.NodeID, HostEpochID: v.source.Permit.HostEpochID}, nil
}

func (v *startupRetirementWireVMM) WithNativeRuntimeInventory(ctx context.Context, consume func(context.Context, fcvm.NativeRuntimeInventory) error) error {
	p := v.source.Permit
	resources := state.RegionalRuntimeResources{RAMMB: 128, VCPU: 1, CPUMillicores: api.DefaultAppCPUMillicores}
	mem, quota := uint64(api.BillableRAMMB(128))<<20, uint64(100000)
	group := fcvm.NativeRuntimeCgroup{Path: fcvm.ParentCgroupFor(p.Grant.Policy.Plan) + "/" + v.source.InstanceKey, Device: 1, Inode: 2, MountID: 3, MemoryMaxBytes: &mem, CPUQuotaUS: &quota, CPUPeriodUS: 100000, PIDs: []int{321}, Populated: true}
	boot := uuid.NewString()
	owner := fcvm.NativeRuntimeObservation{Instance: v.source.InstanceKey, Generation: uuid.NewString(), KernelBootID: boot, Lease: fcvm.Lease{Instance: v.source.InstanceKey, UID: 20000, Plan: p.Grant.Policy.Plan}, Placement: &runtimequota.Placement{Permit: p}, OriginalSource: &v.source, OriginalResources: &resources, PID: 321, StartTime: 99, Authorized: true, VMState: "Running", Machine: &fcvm.Machine{MemSizeMib: 128, VcpuCount: 1}, Cgroup: &group, Executable: &fcvm.NativeRuntimeExecutable{Device: 1, Inode: 7, Bytes: 32, SHA256: strings.Repeat("ab", 32)}}
	return consume(ctx, fcvm.NativeRuntimeInventory{KernelBootID: boot, Owners: []fcvm.NativeRuntimeObservation{owner}, Cgroups: []fcvm.NativeRuntimeCgroup{group}, ManagedUIDTasks: []fcvm.NativeRuntimeTask{{PID: 321, UID: 20000, StartTime: 99}}})
}

func TestRegionalStartupRowRetirementRecoversBothChargesAfterLostReply(t *testing.T) {
	var ins state.Instance
	f := newRetirementCell(t, false, func(store *state.MemStore, app state.App) state.Instance {
		node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "startup-native", TargetURL: "unix:///run/faas/startup-native.sock", Active: true, AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: api.VCPUSlots, VPCPUs: 4})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		ins, err = store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateColdBooting), 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return ins
	})
	source := runtimequota.LaunchSource{Permit: f.p, InstanceKey: ins.ID}
	if err := f.store.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: f.p}, source, state.RegionalRuntimeResources{RAMMB: 128, VCPU: 1, CPUMillicores: api.DefaultAppCPUMillicores}); err != nil {
		t.Fatal(err)
	}
	v := &startupRetirementWireVMM{retirementWireVMM: &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store, firstFence: uuid.NewString(), wantInstance: ins.ID}, source: source}
	var lost atomic.Bool
	interceptor := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		response, err := handler(ctx, req)
		if err == nil && !lost.Swap(true) {
			return nil, status.Error(codes.Unavailable, "lost native stop reply")
		}
		return response, err
	}
	router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, interceptor)
	owner, err := sched.NewRegionalRuntimeRetirement(f.store, runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}, router)
	if err != nil {
		t.Fatal(err)
	}
	ledger := sched.NewNodeLedger()
	engine, err := sched.NewEngine(t.Context(), f.store, ledger, router, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithRegionalRuntimeRetirement(owner)
	if err := engine.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := 2 * api.BillableRAMMB(128)
	ledger.Release(ins.ID)
	if ledger.ResidentRAM() != want {
		t.Fatal("startup observation merged charges or generic row release escaped")
	}
	if err := engine.FailOriginalRegionalRuntime(t.Context(), source, "startup_original_failed"); err == nil || ledger.ResidentRAM() != want {
		t.Fatal("lost native reply released startup charges", err)
	}
	first, err := f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
	if err != nil || first.FenceID == "" || first.ReleasedAt != nil {
		t.Fatal("original first fence was not retained", err)
	}
	_, completed, err := engine.RecoverRegionalRuntimeFailures(t.Context(), state.RegionalRuntimeRetirementCursor{})
	if err != nil || completed != 1 || ledger.ResidentRAM() != 0 || ledger.ResidentFor(ins.ID) {
		t.Fatal("accepted original stop left startup charges", completed, err)
	}
	r, err := f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
	if err != nil || r.ReleasedAt == nil || r.StopEnvelope == nil || r.FenceID != first.FenceID || v.calls.Load() != 2 {
		t.Fatal("recovery lost the original first fences", err)
	}
	stop, err := f.authority.Cells.VerifyCapacityStop(*r.StopEnvelope)
	if err != nil || stop.HostFenceID != v.firstFence || stop.FenceID != first.FenceID || stop.Permit != f.p {
		t.Fatal("accepted signed stop lost original host ownership", err)
	}
}
