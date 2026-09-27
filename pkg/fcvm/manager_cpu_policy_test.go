// adr: 152
package fcvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type cpuLimitCall struct {
	instance string
	cpu      int
}

type cpuPolicyTestVMM struct {
	*fakeVMM
	cpuLimitCalls      []cpuLimitCall
	guestCPULimitCalls []cpuLimitCall
	cpuLimitErr        error
	writeCgroup        bool
}

func (v *cpuPolicyTestVMM) UpdateCPULimit(_ context.Context, lease Lease, cpuMillicores int) error {
	v.mu.Lock()
	v.cpuLimitCalls = append(v.cpuLimitCalls, cpuLimitCall{instance: lease.Instance, cpu: cpuMillicores})
	err := v.cpuLimitErr
	v.mu.Unlock()
	if err != nil {
		return err
	}
	if !v.writeCgroup {
		return nil
	}
	scope := filepath.Join(cgroupRoot, ParentCgroupFor(lease.Plan), PerInstanceScope(lease.Instance))
	return writeAppCPUMaxTo(scope, lease.Plan, cpuMillicores)
}

func (v *cpuPolicyTestVMM) UpdateAppWorkloadCPULimit(_ context.Context, lease Lease, cpuMillicores int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.guestCPULimitCalls = append(v.guestCPULimitCalls, cpuLimitCall{instance: lease.Instance, cpu: cpuMillicores})
	return nil
}

func (v *cpuPolicyTestVMM) cpuLimitCallsSnapshot() []cpuLimitCall {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]cpuLimitCall(nil), v.cpuLimitCalls...)
}

func (v *cpuPolicyTestVMM) guestCPULimitCallsSnapshot() []cpuLimitCall {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]cpuLimitCall(nil), v.guestCPULimitCalls...)
}

func TestManagerUpdateAppCPULimitFansOutOnlyToAppVMs(t *testing.T) {
	vmm := &cpuPolicyTestVMM{fakeVMM: &fakeVMM{}}
	manager := NewManager(&fakeRunner{}, vmm, Paths{}, testFCVersion, nil, nil)
	manager.live["app-a"] = &Instance{AppID: "app-1", WorkloadNames: []string{"main", "metrics"}, Lease: Lease{Instance: "app-a", Plan: api.PlanPro, CPUMillicores: 1000}}
	manager.live["app-b"] = &Instance{AppID: "app-1", Lease: Lease{Instance: "app-b", Plan: api.PlanHobby, CPUMillicores: 500}}
	manager.live["warm"] = &Instance{AppID: "app-1", WorkloadNames: []string{"main", "metrics"}, Paused: true, Lease: Lease{Instance: "warm", Plan: api.PlanPro, CPUMillicores: 500}}
	manager.live["other-app"] = &Instance{AppID: "app-2", Lease: Lease{Instance: "other-app", Plan: api.PlanPro, CPUMillicores: 1000}}
	manager.live["execution"] = &Instance{AppID: "app-1", ExecutionOnly: true, Lease: Lease{Instance: "execution", Plan: api.PlanPro, CPUMillicores: 1000}}
	manager.live["job"] = &Instance{AppID: "app-1", IsJob: true, Lease: Lease{Instance: "job", Plan: api.PlanPro, CPUMillicores: 1000}}

	if err := manager.UpdateAppCPULimit(context.Background(), "app-1", 1, 250); err != nil {
		t.Fatalf("UpdateAppCPULimit: %v", err)
	}
	calls := vmm.cpuLimitCallsSnapshot()
	if len(calls) != 3 {
		t.Fatalf("CPU update calls = %+v, want the three app VMs (including warm snapshot)", calls)
	}
	seen := map[string]int{}
	for _, call := range calls {
		seen[call.instance] = call.cpu
	}
	if seen["app-a"] != 250 || seen["app-b"] != 250 || seen["warm"] != 250 {
		t.Fatalf("CPU update calls = %+v, want app-a/app-b/warm at 250m", calls)
	}
	guestCalls := vmm.guestCPULimitCallsSnapshot()
	if len(guestCalls) != 1 || guestCalls[0].instance != "app-a" || guestCalls[0].cpu != 250 {
		t.Fatalf("guest CPU update calls = %+v, want only the running multi-workload main leaf at 250m", guestCalls)
	}
	for _, instance := range []string{"app-a", "app-b", "warm"} {
		if got := manager.live[instance].Lease.CPUMillicores; got != 250 {
			t.Errorf("%s stored CPU = %d, want 250", instance, got)
		}
	}
	for _, instance := range []string{"other-app", "execution", "job"} {
		if got := manager.live[instance].Lease.CPUMillicores; got == 250 {
			t.Errorf("excluded %s unexpectedly got updated lease", instance)
		}
	}
}

func TestManagerWakeAppliesLatestAppCPULimitBeforePublication(t *testing.T) {
	root := withFakeCgroupRoot(t)
	vmm := &cpuPolicyTestVMM{fakeVMM: &fakeVMM{}, writeCgroup: true}
	manager := newTestManager(&fakeRunner{}, vmm)
	if err := manager.UpdateAppCPULimit(context.Background(), "app-1", 1, 250); err != nil {
		t.Fatalf("seed desired app CPU policy: %v", err)
	}

	inst, err := manager.Wake(context.Background(), WakeRequest{
		Instance: "wake-after-policy", AppID: "app-1", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 2, MemSizeMiB: 128, CPUMillicores: 1000, Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if inst.Lease.CPUMillicores != 250 {
		t.Fatalf("published lease CPU = %d, want latest 250m policy", inst.Lease.CPUMillicores)
	}
	calls := vmm.cpuLimitCallsSnapshot()
	if len(calls) != 1 || calls[0].instance != inst.Lease.Instance || calls[0].cpu != 250 {
		t.Fatalf("CPU updates during wake = %+v, want one 250m update before publication", calls)
	}
	body, err := os.ReadFile(filepath.Join(root, ParentCgroupFor(api.PlanPro), PerInstanceScope(inst.Lease.Instance), "cpu.max"))
	if err != nil {
		t.Fatalf("read live cpu.max: %v", err)
	}
	if got, want := string(body), fmt.Sprintf("%d %d\n", 125000, api.PlanPro.CPUPeriodUS()); got != want {
		t.Fatalf("cpu.max = %q, want %q", got, want)
	}
}

func TestManagerUpdateAppCPULimitRejectsStaleAndConflictingRevisions(t *testing.T) {
	vmm := &cpuPolicyTestVMM{fakeVMM: &fakeVMM{}}
	manager := NewManager(&fakeRunner{}, vmm, Paths{}, testFCVersion, nil, nil)
	manager.live["app-a"] = &Instance{AppID: "app-1", Lease: Lease{Instance: "app-a", Plan: api.PlanPro, CPUMillicores: 1000}}

	if err := manager.UpdateAppCPULimit(context.Background(), "app-1", 2, 500); err != nil {
		t.Fatalf("apply revision 2: %v", err)
	}
	if err := manager.UpdateAppCPULimit(context.Background(), "app-1", 1, 250); err == nil {
		t.Fatal("stale revision 1 unexpectedly succeeded")
	}
	if err := manager.UpdateAppCPULimit(context.Background(), "app-1", 2, 250); err == nil {
		t.Fatal("conflicting CPU quota for revision 2 unexpectedly succeeded")
	}
	if got := vmm.cpuLimitCallsSnapshot(); len(got) != 1 || got[0].cpu != 500 {
		t.Fatalf("CPU updates after rejected revisions = %+v, want only revision 2 at 500m", got)
	}
}
