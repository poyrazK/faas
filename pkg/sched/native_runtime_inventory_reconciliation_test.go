package sched

// adr: 649
import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func nativeReconciliationFixture(t *testing.T) (state.RegionalRuntimeInventoryEntry, fcvm.NativeRuntimeInventorySample) {
	t.Helper()
	_, _, _, source, resources := regionalAccountingFixture(t)
	now := time.Now()
	entry := state.RegionalRuntimeInventoryEntry{Receipt: state.RegionalRuntimeCapacityReceipt{Permit: source.Permit, IssuedAt: &now}, Source: &source, Resources: resources}
	boot := uuid.NewString()
	mem, cpu := uint64(api.BillableRAMMB(resources.RAMMB))<<20, uint64(100000)
	group := fcvm.NativeRuntimeCgroup{Path: fcvm.ParentCgroupFor(source.Permit.Grant.Policy.Plan) + "/" + source.InstanceKey, Device: 1, Inode: 2, MountID: 3, MemoryMaxBytes: &mem, CPUQuotaUS: &cpu, CPUPeriodUS: 100000, PIDs: []int{321}, Populated: true}
	owner := fcvm.NativeRuntimeObservation{Instance: source.InstanceKey, Generation: uuid.NewString(), KernelBootID: boot, Lease: fcvm.Lease{Instance: source.InstanceKey, Plan: source.Permit.Grant.Policy.Plan, UID: 20000}, Placement: &runtimequota.Placement{Permit: source.Permit}, OriginalResources: &resources, PID: 321, StartTime: 99, VMState: "Running", Machine: &fcvm.Machine{MemSizeMib: resources.RAMMB, VcpuCount: resources.VCPU}, Cgroup: &group, Authorized: true}
	return entry, fcvm.NativeRuntimeInventorySample{Version: 1, Host: runtimequota.HostEpoch{NodeID: source.Permit.NodeID, HostEpochID: source.Permit.HostEpochID}, Challenge: uuid.NewString(), Inventory: fcvm.NativeRuntimeInventory{KernelBootID: boot, Owners: []fcvm.NativeRuntimeObservation{owner}, Cgroups: []fcvm.NativeRuntimeCgroup{group}, ManagedUIDTasks: []fcvm.NativeRuntimeTask{{PID: 321, UID: 20000, StartTime: 99}}}}
}

func TestNativeRegionalInventoryMatchesOnlyOriginalObservedHold(t *testing.T) {
	cases := map[string]func(*state.RegionalRuntimeInventoryEntry, *fcvm.NativeRuntimeInventorySample){
		"host-epoch": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Host.HostEpochID = uuid.NewString()
		},
		"fenced": func(e *state.RegionalRuntimeInventoryEntry, _ *fcvm.NativeRuntimeInventorySample) {
			e.Receipt.FenceID = uuid.NewString()
		},
		"missing-source": func(e *state.RegionalRuntimeInventoryEntry, _ *fcvm.NativeRuntimeInventorySample) { e.Source = nil },
		"historical-resources": func(e *state.RegionalRuntimeInventoryEntry, _ *fcvm.NativeRuntimeInventorySample) {
			e.Resources = state.RegionalRuntimeResources{}
		},
		"missing-owner": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners = nil
		},
		"duplicate-owner": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners = append(s.Inventory.Owners, s.Inventory.Owners[0])
		},
		"lookup-spelling": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].Instance = "wrong"
		},
		"run": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].Placement.Permit.RunID = uuid.NewString()
		},
		"original-resources": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].OriginalResources.RAMMB++
		},
		"boot": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].KernelBootID = uuid.NewString()
		},
		"revoked": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].Revoked = true
		},
		"removed": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].ResourcesRemoved = true
		},
		"unknown": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].Unresolved = "unresolved"
		},
		"machine": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].Machine.VcpuCount++
		},
		"memory-limit": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].Cgroup.MemoryMaxBytes = nil
		},
		"cpu-limit": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Owners[0].Cgroup.CPUQuotaUS = nil
		},
		"independent-cgroup": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Cgroups = nil
		},
		"duplicate-cgroup": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Cgroups = append(s.Inventory.Cgroups, s.Inventory.Cgroups[0])
		},
		"cgroup-identity": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.Cgroups[0].Inode++
		},
		"missing-task": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.ManagedUIDTasks = nil
		},
		"reused-pid": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.ManagedUIDTasks[0].StartTime++
		},
		"duplicate-task": func(_ *state.RegionalRuntimeInventoryEntry, s *fcvm.NativeRuntimeInventorySample) {
			s.Inventory.ManagedUIDTasks = append(s.Inventory.ManagedUIDTasks, s.Inventory.ManagedUIDTasks[0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e, s := nativeReconciliationFixture(t)
			if reason := regionalRuntimeObservationIssue(e, s); reason != "" {
				t.Fatal("fixture did not match", reason)
			}
			mutate(&e, &s)
			if reason := regionalRuntimeObservationIssue(e, s); reason == "" {
				t.Fatal("uncertain ownership matched")
			}
		})
	}
}

type inventoryReadingVMM struct {
	*fakeVMM
	sample fcvm.NativeRuntimeInventorySample
	err    error
	calls  int
}

func (v *inventoryReadingVMM) NativeRuntimeInventory(context.Context, string) (fcvm.NativeRuntimeInventorySample, error) {
	v.calls++
	return v.sample, v.err
}

func TestNativeRegionalInventoryPageRetainsHoldsAndAdvancesOnUncertainty(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "matched", true: "lost-reply"}[lost], func(t *testing.T) {
			s, app, _, source, resources := regionalAccountingFixture(t)
			issueAccountingFixture(t, s, source, resources)
			if err := s.DeleteApp(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
			_, sample := nativeReconciliationFixture(t)
			o := &sample.Inventory.Owners[0]
			o.Instance, o.Lease.Instance = source.InstanceKey, source.InstanceKey
			o.Placement.Permit = source.Permit
			o.Cgroup.Path = fcvm.ParentCgroupFor(source.Permit.Grant.Policy.Plan) + "/" + source.InstanceKey
			sample.Inventory.Cgroups[0].Path = o.Cgroup.Path
			sample.Host = runtimequota.HostEpoch{NodeID: source.Permit.NodeID, HostEpochID: source.Permit.HostEpochID}
			v := &inventoryReadingVMM{fakeVMM: &fakeVMM{}, sample: sample}
			if lost {
				v.err = errors.New("lost reply")
			}
			e := newEngine(t, s, v, &fakeNotifier{}, "1.10.0")
			if err := e.SeedLedger(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, _ := s.RegionalRuntimeCapacity(t.Context(), app.ID, source.Permit.ID)
			after, report, err := e.ObserveRegionalRuntimeInventoryPage(t.Context(), state.RegionalRuntimeRetirementCursor{})
			if (err != nil) != lost || after.PermitID != source.Permit.ID || v.calls != 1 || len(report.Matches) != map[bool]int{false: 1, true: 0}[lost] || len(report.Issues) != map[bool]int{false: 0, true: 1}[lost] {
				t.Fatal(after, report, err)
			}
			next, empty, err := e.ObserveRegionalRuntimeInventoryPage(t.Context(), after)
			if err != nil || next != (state.RegionalRuntimeRetirementCursor{}) || empty.Nodes != 0 {
				t.Fatal("cursor failed to reset", err)
			}
			retained, _ := s.RegionalRuntimeCapacity(t.Context(), app.ID, source.Permit.ID)
			if !reflect.DeepEqual(before, retained) || e.ledger.ResidentRAM() != resources.RAMMB+api.PerVMOverheadMB {
				t.Fatal("advisory observation changed original accounting")
			}
			head, err := s.RegionalRuntimeQuotaHead(t.Context(), app.ID)
			if err != nil || head.AdmissionEnabled {
				t.Fatal("observation activated regional head", err)
			}
		})
	}
}
