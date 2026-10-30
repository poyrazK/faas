// adr: 20261027090010
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

// agentRunTestVMM serves agentrunproto on an in-memory pipe in place of the
// guest.
type agentRunTestVMM struct {
	*fakeVMM
	dialErr error
}

func (v *agentRunTestVMM) DialAgentRun(ctx context.Context, _ Lease) (net.Conn, error) {
	if v.dialErr != nil {
		return nil, v.dialErr
	}
	host, guest := net.Pipe()
	// The guest outlives the dial call, as a real VM does.
	guestCtx := context.WithoutCancel(ctx)
	go func() {
		defer guest.Close()
		_ = agentrunproto.Serve(guestCtx, guest, func(_ context.Context, req agentrunproto.Request, stdout, _ io.Writer, _ <-chan struct{}) (agentrunproto.Result, error) {
			fmt.Fprintf(stdout, "ran %s", req.Command[0])
			return agentrunproto.Result{Status: agentrunproto.StatusSucceeded, Result: json.RawMessage(`{"ok":true}`)}, nil
		})
	}()
	return host, nil
}

func agentRunTestRequest() agentrunproto.Request {
	return agentrunproto.Request{Version: agentrunproto.Version, RunID: "run-1", Command: []string{"codex"}, TimeoutSec: 30,
		MaxLogBytes: 1024, MaxResultBytes: 1024, MaxOutputFiles: 1, MaxOutputBytes: 1024}
}

func agentRunTestManager(t *testing.T, vmm VMM, inst *Instance) *Manager {
	t.Helper()
	m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
	lease, err := m.alloc.Acquire("agent-vm-1")
	if err != nil {
		t.Fatal(err)
	}
	lease.Networkless = true
	inst.Lease = lease
	m.live["agent-vm-1"] = inst
	return m
}

func TestRunAgentStreamsAndDestroys(t *testing.T) {
	vmm := &agentRunTestVMM{fakeVMM: &fakeVMM{}}
	m := agentRunTestManager(t, vmm, &Instance{ExecutionOnly: true, AgentRunOnly: true})
	var streamed string
	result, err := m.RunAgent(context.Background(), "agent-vm-1", agentRunTestRequest(), func(_ context.Context, _ string, chunk []byte) error {
		streamed += string(chunk)
		return nil
	}, nil)
	if err != nil || result.Status != agentrunproto.StatusSucceeded || string(result.Result) != `{"ok":true}` {
		t.Fatalf("result %+v, %v", result, err)
	}
	if streamed != "ran codex" {
		t.Fatalf("streamed %q", streamed)
	}
	if got := m.LiveCount(); got != 0 {
		t.Fatalf("live count %d after the run, want 0", got)
	}
}

func TestRunAgentDestroysAfterDialFailure(t *testing.T) {
	vmm := &agentRunTestVMM{fakeVMM: &fakeVMM{}, dialErr: errors.New("guest never listened")}
	m := agentRunTestManager(t, vmm, &Instance{ExecutionOnly: true, AgentRunOnly: true})
	if _, err := m.RunAgent(context.Background(), "agent-vm-1", agentRunTestRequest(), nil, nil); err == nil {
		t.Fatal("dial failure not reported")
	}
	if got := m.LiveCount(); got != 0 {
		t.Fatalf("live count %d after a failed dial, want 0", got)
	}
}

// Agent run VMs and execution VMs share the execution-only boot path, but
// neither protocol accepts the other's instance.
func TestAgentRunAndExecutionInstancesStayApart(t *testing.T) {
	vmm := &agentRunTestVMM{fakeVMM: &fakeVMM{}}
	m := agentRunTestManager(t, vmm, &Instance{ExecutionOnly: true})
	if _, err := m.RunAgent(context.Background(), "agent-vm-1", agentRunTestRequest(), nil, nil); !errors.Is(err, ErrAgentRunInstanceNotFound) {
		t.Fatalf("execution instance accepted for an agent run: %v", err)
	}
	m = agentRunTestManager(t, vmm, &Instance{ExecutionOnly: true, AgentRunOnly: true})
	_, err := m.ExecuteExecution(context.Background(), "agent-vm-1", executionproto.Request{Version: executionproto.Version,
		ExecutionID: "e", Runtime: api.ExecutionRuntimeNode22, Source: "1", Input: json.RawMessage(`null`), TimeoutMS: 1000,
		MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone})
	if !errors.Is(err, ErrExecutionInstanceNotFound) {
		t.Fatalf("agent run instance accepted for an execution: %v", err)
	}
	m = agentRunTestManager(t, vmm, &Instance{})
	if _, err := m.RunAgent(context.Background(), "agent-vm-1", agentRunTestRequest(), nil, nil); !errors.Is(err, ErrAgentRunInstanceNotFound) {
		t.Fatalf("app instance accepted for an agent run: %v", err)
	}
}

func TestWakeAgentRunRejectsIncompleteRequests(t *testing.T) {
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil)
	full := AgentRunWakeRequest{Instance: "a", AccountID: "acct", Plan: api.PlanPro, BaseKey: "base/agent-base-amd64.ext4",
		LayerKey: "layer/agent.ext4", VcpuCount: 1, MemSizeMiB: 1024, CPUMillicores: 1000}
	for name, mutate := range map[string]func(*AgentRunWakeRequest){
		"no instance": func(r *AgentRunWakeRequest) { r.Instance = "" },
		"no account":  func(r *AgentRunWakeRequest) { r.AccountID = "" },
		"bad plan":    func(r *AgentRunWakeRequest) { r.Plan = "gold" },
		"no base":     func(r *AgentRunWakeRequest) { r.BaseKey = "" },
		"no layer":    func(r *AgentRunWakeRequest) { r.LayerKey = "" },
		"no memory":   func(r *AgentRunWakeRequest) { r.MemSizeMiB = 0 },
		"bad relay":   func(r *AgentRunWakeRequest) { r.Relay = &AgentRunRelay{RunID: "run-1"} },
	} {
		req := full
		mutate(&req)
		if _, err := m.WakeAgentRun(context.Background(), req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Firecracker 1.7's --config-file parser rejects "network-interfaces": null,
// so a networkless guest's config carries an empty list.
func TestNetworklessConfigListsNoInterfaces(t *testing.T) {
	cfg := BuildColdBootConfig(ColdBootSpec{Networkless: true, VcpuCount: 1, MemSizeMiB: 512}, 3)
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"network-interfaces":[]`) {
		t.Fatalf("networkless config: %s", body)
	}
}

// Only an agent run VM with a relay principal has a relay identity, and it
// is the principal the wake bound.
func TestAgentRunRelayIdentity(t *testing.T) {
	relay := &AgentRunRelay{RunID: "11111111-2222-3333-4444-555555555555", LeaseToken: "66666666-2222-3333-4444-555555555555",
		TenantID: "77777777-2222-3333-4444-555555555555", AgentID: "88888888-2222-3333-4444-555555555555", AgentVersion: 2}
	m := agentRunTestManager(t, &fakeVMM{}, &Instance{AgentRunOnly: true, ExecutionOnly: true, AccountID: "acct", AgentRunRelay: relay})
	account, got, err := m.AgentRunRelayIdentity("agent-vm-1")
	if err != nil || account != "acct" || got != *relay {
		t.Fatalf("identity %q %+v %v", account, got, err)
	}
	m.live["agent-vm-1"].AgentRunRelay = nil
	if _, _, err := m.AgentRunRelayIdentity("agent-vm-1"); err == nil {
		t.Fatal("a run without a relay principal got a relay identity")
	}
	m.live["agent-vm-1"].AgentRunRelay, m.live["agent-vm-1"].AgentRunOnly = relay, false
	if _, _, err := m.AgentRunRelayIdentity("agent-vm-1"); err == nil {
		t.Fatal("a VM that is not an agent run got a relay identity")
	}
}

// The input drive is read-only and follows the base and the agent layer,
// so the guest finds it as drive 2; only networkless single-layer guests
// may carry one.
func TestColdBootInputDrive(t *testing.T) {
	spec := ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 1, MemSizeMiB: 512, Networkless: true, SkipReady: true,
		AgentInput: true, InputKey: "agents/inputs/images/acct/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.img"}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	drives := BuildColdBootConfig(spec, 3).Drives
	if len(drives) != 3 || drives[2].DriveID != DriveAgentInput || !drives[2].IsReadOnly || drives[2].PathOnHost != spec.InputKey {
		t.Fatalf("drives %+v", drives)
	}
	networked := spec
	networked.Networkless, networked.Tap = false, "tap0"
	if err := networked.Validate(); err == nil {
		t.Fatal("a networked guest got an input drive")
	}
	stray := spec
	stray.AgentInput = false
	if err := stray.Validate(); err == nil {
		t.Fatal("an input image without the input drive was accepted")
	}
	empty := spec
	empty.InputKey = ""
	if drives := BuildColdBootConfig(empty, 3).Drives; len(drives) != 3 || drives[2].DriveID != DriveAgentInput || drives[2].PathOnHost != "" {
		t.Fatalf("a run without a checkout still gets the zero input drive: %+v", drives)
	}
}

// The staged input drive always has the fixed capacity, the image first,
// and sits beside the writable layer on disk.
func TestFillAgentInput(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image.img")
	if err := os.WriteFile(image, []byte("GRGLAIN1 image"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{image, ""} {
		f, err := os.CreateTemp(dir, "input-*")
		if err != nil {
			t.Fatal(err)
		}
		if err := errors.Join(fillAgentInput(f, src), f.Close()); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(f.Name())
		if err != nil || int64(len(got)) != agentrunproto.InputDriveBytes {
			t.Fatalf("%q: %d bytes, %v", src, len(got), err)
		}
		if src != "" && string(got[:14]) != "GRGLAIN1 image" {
			t.Fatalf("image not first: %q", got[:14])
		}
	}
	if agentInputStagingDir([]Drive{{DriveID: DriveBase, PathOnHost: "/a/base", IsReadOnly: true}, {DriveID: DriveLayer, PathOnHost: "/cache/layer.ext4"}}) != "/cache" {
		t.Fatal("the input drive is not staged beside the writable layer")
	}
}
