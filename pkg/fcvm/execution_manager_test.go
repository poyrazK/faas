// adr: 171
package fcvm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestManagerExecuteExecutionRejectsOrdinaryAppInstance(t *testing.T) {
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil)
	m.live["app-vm-1"] = &Instance{Lease: Lease{Instance: "app-vm-1"}}

	_, err := m.ExecuteExecution(context.Background(), "app-vm-1", executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: "exec-1",
		Runtime:     api.ExecutionRuntimeNode22,
		Source:      "1",
		Input:       json.RawMessage(`null`),
		TimeoutMS:   1000,
		MaxOutput:   1024,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if !errors.Is(err, ErrExecutionInstanceNotFound) {
		t.Fatalf("error = %v, want ErrExecutionInstanceNotFound", err)
	}
}

type executionTestVMM struct{ *fakeVMM }

func (v *executionTestVMM) DialExecution(_ context.Context, _ Lease) (*ExecutionSession, error) {
	host, guest := net.Pipe()
	go func() {
		defer guest.Close()
		var header [8]byte
		if _, err := io.ReadFull(guest, header[:]); err != nil {
			return
		}
		bodyLen := binary.BigEndian.Uint32(header[4:])
		if bodyLen > 0 {
			_, _ = io.CopyN(io.Discard, guest, int64(bodyLen))
		}
		body, _ := json.Marshal(executionproto.Result{
			Status: api.ExecutionStatusSucceeded,
			Result: json.RawMessage(`{"ok":true}`),
		})
		binary.BigEndian.PutUint32(header[:4], executionproto.FrameResult)
		binary.BigEndian.PutUint32(header[4:], uint32(len(body)))
		_, _ = guest.Write(header[:])
		_, _ = guest.Write(body)
	}()
	return NewExecutionSession(host)
}

func TestManagerExecuteExecutionDestroysExecutionOnlyInstance(t *testing.T) {
	vmm := &executionTestVMM{fakeVMM: &fakeVMM{}}
	m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
	m.live["exec-vm-1"] = &Instance{
		Lease:         Lease{Instance: "exec-vm-1"},
		ExecutionOnly: true,
	}

	result, err := m.ExecuteExecution(context.Background(), "exec-vm-1", executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: "exec-1",
		Runtime:     api.ExecutionRuntimeNode22,
		Source:      "1",
		Input:       json.RawMessage(`null`),
		TimeoutMS:   1000,
		MaxOutput:   1024,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if err != nil {
		t.Fatalf("ExecuteExecution: %v", err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != `{"ok":true}` {
		t.Fatalf("result = %+v", result)
	}
	if got := m.LiveCount(); got != 0 {
		t.Fatalf("live count = %d, want 0 after one-shot teardown", got)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if len(vmm.destroyedWithExport) != 1 || vmm.destroyedWithExport[0] != "exec-vm-1" {
		t.Fatalf("destroy calls = %v, want [exec-vm-1]", vmm.destroyedWithExport)
	}
}

type boundedExecutionVMM struct {
	*fakeVMM
	deadlines   chan time.Time
	guestClosed chan struct{}
}

func (v *boundedExecutionVMM) DialExecution(ctx context.Context, _ Lease) (*ExecutionSession, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("execution dial context has no deadline")
	}
	v.deadlines <- deadline
	host, guest := net.Pipe()
	go func() {
		defer close(v.guestClosed)
		defer guest.Close()
		var header [8]byte
		if _, err := io.ReadFull(guest, header[:]); err != nil {
			return
		}
		bodyLen := binary.BigEndian.Uint32(header[4:])
		if bodyLen > 0 {
			if _, err := io.CopyN(io.Discard, guest, int64(bodyLen)); err != nil {
				return
			}
		}
		// Do not produce a result. The host must cancel this exchange at the
		// request deadline and close the stream before teardown proceeds.
		_, _ = io.Copy(io.Discard, guest)
	}()
	return NewExecutionSession(host)
}

func TestManagerExecuteExecutionBoundsDirectCallToRequestTimeout(t *testing.T) {
	vmm := &boundedExecutionVMM{
		fakeVMM:     &fakeVMM{},
		deadlines:   make(chan time.Time, 1),
		guestClosed: make(chan struct{}),
	}
	m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
	m.live["exec-timeout-vm"] = &Instance{
		Lease:         Lease{Instance: "exec-timeout-vm"},
		ExecutionOnly: true,
	}
	started := time.Now()
	_, err := m.ExecuteExecution(context.Background(), "exec-timeout-vm", executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: "exec-timeout",
		Runtime:     api.ExecutionRuntimeNode22,
		Source:      "1",
		Input:       json.RawMessage(`null`),
		TimeoutMS:   100,
		MaxOutput:   1024,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	select {
	case deadline := <-vmm.deadlines:
		if deadline.After(started.Add(500 * time.Millisecond)) {
			t.Fatalf("dial deadline = %s, want within 500ms of call", deadline)
		}
	case <-time.After(time.Second):
		t.Fatal("dialer did not receive a deadline")
	}
	select {
	case <-vmm.guestClosed:
	case <-time.After(time.Second):
		t.Fatal("guest stream remained open after request timeout")
	}
	if got := m.LiveCount(); got != 0 {
		t.Fatalf("live count = %d, want 0 after timeout teardown", got)
	}
}

// adr: 171 — the dedicated constructor allocates lifecycle identity and a
// cgroup-backed VM but never invokes tenant netns/veth/tap setup commands.
func TestManagerWakeExecutionIsNetworkless(t *testing.T) {
	runner := &fakeRunner{}
	vmm := &fakeVMM{}
	m := NewManager(runner, vmm, Paths{Kernel: "kernel/test"}, "1.0.0", nil, nil)
	inst, err := m.WakeExecution(context.Background(), ExecutionWakeRequest{
		Instance: "exec-wake-1", AccountID: "acct-1", Plan: api.PlanPro,
		Runtime: string(api.ExecutionRuntimeNode22), KernelKey: "kernel/test",
		BaseKey: "base/test", LayerKey: "layer/test", VcpuCount: 2,
		MemSizeMiB: 256, CPUMillicores: 500,
	})
	if err != nil {
		t.Fatalf("WakeExecution: %v", err)
	}
	if inst == nil || !inst.ExecutionOnly || !inst.Lease.Networkless {
		t.Fatalf("execution instance = %#v, want execution-only networkless lease", inst)
	}
	vmm.mu.Lock()
	spec := vmm.coldBootSpecs[len(vmm.coldBootSpecs)-1]
	vmm.mu.Unlock()
	if !spec.Networkless || spec.Tap != "" {
		t.Fatalf("cold-boot spec = %#v, want networkless empty tap", spec)
	}
	if !spec.SkipReady {
		t.Fatalf("cold-boot spec SkipReady = false, want true for execution guest")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("network commands = %#v, want none", runner.commands)
	}
	if err := m.Destroy(context.Background(), inst.Lease.Instance); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestManagerWakeExecutionRestoreSkipsAppReadiness(t *testing.T) {
	runner := &fakeRunner{}
	vmm := &fakeVMM{}
	m := NewManager(runner, vmm, Paths{Kernel: "kernel/test"}, "1.0.0", nil, nil)
	inst, err := m.WakeExecution(context.Background(), ExecutionWakeRequest{
		Instance: "exec-restore-1", AccountID: "acct-1", Plan: api.PlanPro,
		Runtime: string(api.ExecutionRuntimeNode22), KernelKey: "kernel/test",
		BaseKey: "base/test", LayerKey: "layer/test", VcpuCount: 2,
		MemSizeMiB: 256, CPUMillicores: 500,
		Snapshot: &Snapshot{
			DeploymentID: "exec-deployment-1", FCVersion: "1.0.0",
			StorageKey: "snap/exec-deployment-1/mem", VMStatePath: "/snap/state",
			Networkless: true,
		},
	})
	if err != nil {
		t.Fatalf("WakeExecution restore: %v", err)
	}
	defer func() { _ = m.Destroy(context.Background(), inst.Lease.Instance) }()

	vmm.mu.Lock()
	if len(vmm.restoreSpecs) != 1 {
		vmm.mu.Unlock()
		t.Fatalf("restore calls = %d, want 1", len(vmm.restoreSpecs))
	}
	spec := vmm.restoreSpecs[0]
	vmm.mu.Unlock()
	if !spec.Networkless {
		t.Fatalf("restore spec Networkless = false, want true")
	}
	if !spec.SkipReady {
		t.Fatalf("restore spec SkipReady = false, want true for execution guest")
	}
	if spec.Tap != "" {
		t.Fatalf("restore spec Tap = %q, want empty for execution guest", spec.Tap)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("network commands = %#v, want none", runner.commands)
	}
}
