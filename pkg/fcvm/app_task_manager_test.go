package fcvm

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestManagerExecuteAppTaskRejectsOrdinaryAppInstance(t *testing.T) {
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil)
	m.live["app-vm-1"] = &Instance{Lease: Lease{Instance: "app-vm-1"}}
	if _, err := m.ExecuteAppTask(context.Background(), "app-vm-1", appTaskProtocolRequest("task-1")); !errors.Is(err, ErrAppTaskInstanceNotFound) {
		t.Fatalf("error = %v, want ErrAppTaskInstanceNotFound", err)
	}
}

type appTaskTestVMM struct{ *fakeVMM }

func (v *appTaskTestVMM) DialAppTask(_ context.Context, _ Lease) (*AppTaskSession, error) {
	host, guest := net.Pipe()
	go func() {
		defer guest.Close()
		_ = apptaskproto.Serve(context.Background(), guest, func(context.Context, apptaskproto.Request, *apptaskproto.OutputWriter, *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
			exit := 0
			return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit}, nil
		})
	}()
	return NewAppTaskSession(host)
}

func TestManagerExecuteAppTaskDestroysInstance(t *testing.T) {
	vmm := &appTaskTestVMM{fakeVMM: &fakeVMM{}}
	m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
	m.live["task-vm-1"] = &Instance{Lease: Lease{Instance: "task-vm-1"}, AppTaskOnly: true}
	result, err := m.ExecuteAppTask(context.Background(), "task-vm-1", appTaskProtocolRequest("task-1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if got := m.LiveCount(); got != 0 {
		t.Fatalf("live count = %d, want 0", got)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if len(vmm.destroyedWithExport) != 1 || vmm.destroyedWithExport[0] != "task-vm-1" {
		t.Fatalf("destroy calls = %v", vmm.destroyedWithExport)
	}
}

func TestManagerExecuteAppTaskDestroysWhenDialerIsUnavailable(t *testing.T) {
	vmm := &fakeVMM{}
	m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
	m.live["task-vm-unsupported"] = &Instance{Lease: Lease{Instance: "task-vm-unsupported"}, AppTaskOnly: true}
	_, err := m.ExecuteAppTask(context.Background(), "task-vm-unsupported", appTaskProtocolRequest("task-1"))
	if !errors.Is(err, ErrAppTaskNotConfigured) {
		t.Fatalf("error = %v, want ErrAppTaskNotConfigured", err)
	}
	if got := m.LiveCount(); got != 0 {
		t.Fatalf("live count = %d, want 0", got)
	}
}

func TestManagerWakeAppTaskKeepsAppNetworkAndStagesMarker(t *testing.T) {
	runner := &fakeRunner{}
	vmm := &fakeVMM{}
	m := NewManager(runner, vmm, Paths{Kernel: "kernel/test"}, "1.0.0", nil, nil)
	inst, err := m.WakeAppTask(context.Background(), AppTaskWakeRequest{WakeRequest: WakeRequest{
		Instance: "task-wake-1", AccountID: "acct-1", AppID: "app-1", DeploymentID: "dep-1",
		Plan: api.PlanPro, Runtime: "node22", BaseKey: "base/test", LayerKey: "layer/test",
		VcpuCount: 2, MemSizeMiB: 256, CPUMillicores: 500,
		Sidecars: []WorkloadSpec{{Name: "metrics", StorageKey: "sidecar/test"}},
	}})
	if err != nil {
		t.Fatalf("WakeAppTask: %v", err)
	}
	defer func() { _ = m.Destroy(context.Background(), inst.Lease.Instance) }()
	if !inst.AppTaskOnly || inst.ExecutionOnly || inst.Lease.Networkless {
		t.Fatalf("instance = %#v", inst)
	}
	vmm.mu.Lock()
	spec := vmm.coldBootSpecs[len(vmm.coldBootSpecs)-1]
	vmm.mu.Unlock()
	if !spec.AppTask || !spec.SkipReady || spec.Networkless || spec.Tap == "" {
		t.Fatalf("cold boot spec = %#v", spec)
	}
	if len(spec.Workloads) != 0 {
		t.Fatalf("app task staged formation sidecars: %#v", spec.Workloads)
	}
	if len(runner.commands) == 0 {
		t.Fatal("app task skipped normal tenant network setup")
	}
}

func TestManagerWakeAppTaskRejectsSnapshot(t *testing.T) {
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: "kernel/test"}, "1.0.0", nil, nil)
	_, err := m.WakeAppTask(context.Background(), AppTaskWakeRequest{WakeRequest: WakeRequest{
		Instance: "task-wake-1", AccountID: "acct-1", AppID: "app-1", DeploymentID: "dep-1",
		Plan: api.PlanPro, BaseKey: "base/test", LayerKey: "layer/test", VcpuCount: 2,
		MemSizeMiB: 256, CPUMillicores: 500, Snapshot: &Snapshot{FCVersion: "1.0.0"},
	}})
	if err == nil {
		t.Fatal("snapshot-backed app task wake was accepted")
	}
}
