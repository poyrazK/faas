package fcvm

// adr: 20261027090013 — a sandbox session VM serves only its own session's
// calls and no other kind of exchange; unlike other execution-only VMs it
// may be quiet-paused and frozen between calls.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

type sandboxTestVMM struct {
	*fakeVMM
	dials int
}

func (v *sandboxTestVMM) DialSandbox(context.Context, string) (net.Conn, error) {
	v.dials++
	host, guest := net.Pipe()
	_ = guest.Close()
	return host, nil
}

func TestWakeSandboxRejectsIncompleteRequests(t *testing.T) {
	m := NewManager(&fakeRunner{}, &sandboxTestVMM{fakeVMM: &fakeVMM{}}, Paths{}, "1.0.0", nil, nil)
	full := SandboxWakeRequest{Instance: "s", SandboxID: "sbx-1", AccountID: "acct", Plan: api.PlanPro, BaseKey: "base/runner-agent-amd64.ext4",
		LayerKey: "base/sandbox-layer-v1-amd64.ext4", VcpuCount: 1, MemSizeMiB: 1024, CPUMillicores: 1000}
	for name, mutate := range map[string]func(*SandboxWakeRequest){
		"no instance":    func(r *SandboxWakeRequest) { r.Instance = "" },
		"no sandbox":     func(r *SandboxWakeRequest) { r.SandboxID = "" },
		"bad plan":       func(r *SandboxWakeRequest) { r.Plan = "gold" },
		"no layer":       func(r *SandboxWakeRequest) { r.LayerKey = "" },
		"no memory":      func(r *SandboxWakeRequest) { r.MemSizeMiB = 0 },
		"negative pause": func(r *SandboxWakeRequest) { r.QuietPauseAfter = -time.Second },
	} {
		req := full
		mutate(&req)
		if _, err := m.WakeSandbox(context.Background(), req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil).WakeSandbox(context.Background(), full); err == nil {
		t.Error("a VMM that cannot dial sandboxes woke one")
	}
}

func TestSandboxInstancesServeOnlyTheirSession(t *testing.T) {
	vmm := &sandboxTestVMM{fakeVMM: &fakeVMM{}}
	m := agentRunTestManager(t, vmm, &Instance{ExecutionOnly: true, SandboxID: "sbx-1"})
	conn, err := m.DialSandboxSession(context.Background(), "agent-vm-1", "sbx-1")
	if err != nil {
		t.Fatalf("own session: %v", err)
	}
	_ = conn.Close()
	for name, tc := range map[string]struct{ instance, sandbox string }{
		"another session": {"agent-vm-1", "sbx-2"},
		"no session":      {"agent-vm-1", ""},
		"no instance":     {"gone", "sbx-1"},
	} {
		if _, err := m.DialSandboxSession(context.Background(), tc.instance, tc.sandbox); !errors.Is(err, ErrSandboxNotFound) {
			t.Errorf("%s: %v, want ErrSandboxNotFound", name, err)
		}
	}
	_, err = m.ExecuteExecution(context.Background(), "agent-vm-1", executionproto.Request{Version: executionproto.Version,
		ExecutionID: "e", Runtime: api.ExecutionRuntimeNode22, Source: "1", Input: json.RawMessage(`null`), TimeoutMS: 1000,
		MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone})
	if !errors.Is(err, ErrExecutionInstanceNotFound) {
		t.Fatalf("sandbox instance accepted for an execution: %v", err)
	}
	if _, _, err := m.RunAgent(context.Background(), "agent-vm-1", agentRunTestRequest(), nil, nil); !errors.Is(err, ErrAgentRunInstanceNotFound) {
		t.Fatalf("sandbox instance accepted for an agent run: %v", err)
	}
	m = agentRunTestManager(t, vmm, &Instance{ExecutionOnly: true})
	if _, err := m.DialSandboxSession(context.Background(), "agent-vm-1", "sbx-1"); !errors.Is(err, ErrSandboxNotFound) {
		t.Fatalf("execution instance served a sandbox call: %v", err)
	}
}

func TestSandboxInstancesMayPauseAndFreeze(t *testing.T) {
	sandbox := &Instance{ExecutionOnly: true, SandboxID: "sbx-1", QuietPauseAfter: 2 * time.Second}
	execution := &Instance{ExecutionOnly: true, QuietPauseAfter: 2 * time.Second}
	if got := freezeRefusal(sandbox); got != "" {
		t.Errorf("sandbox freeze refusal = %q", got)
	}
	if got := freezeRefusal(execution); got != "execution instance" {
		t.Errorf("execution freeze refusal = %q", got)
	}
	if got := quietPauseAfterFor(WakeRequest{ExecutionOnly: true, sandboxID: "sbx-1", QuietPauseAfter: 2 * time.Second}, false); got != 2*time.Second {
		t.Errorf("sandbox quiet pause = %v", got)
	}
	if got := quietPauseAfterFor(WakeRequest{ExecutionOnly: true, QuietPauseAfter: 2 * time.Second}, false); got != 0 {
		t.Errorf("execution quiet pause = %v", got)
	}
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil)
	m.mu.Lock()
	sandboxOK, executionOK := m.quietEligibleLocked("s", sandbox), m.quietEligibleLocked("e", execution)
	m.mu.Unlock()
	if !sandboxOK || executionOK {
		t.Errorf("quiet eligible: sandbox %v, execution %v", sandboxOK, executionOK)
	}
}
