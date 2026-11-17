// adr: 20261116182615 §1–§2 — a browser session runs vmmd's own browser
// commands through template_start after a cold boot, refuses commands or an
// image of its own, and a capture prewarms Chromium with a plain exec.
package fcvm

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// browserGuestVMM answers sandbox calls like guest-init and records them.
type browserGuestVMM struct {
	*fakeVMM
	mu       sync.Mutex
	requests []sandboxproto.Request
	exitCode int
}

func (v *browserGuestVMM) DialSandbox(ctx context.Context, _ string) (net.Conn, error) {
	host, guest := net.Pipe()
	go func() {
		_ = sandboxproto.Serve(context.WithoutCancel(ctx), guest, func(_ context.Context, req sandboxproto.Request, _ *sandboxproto.Call) (any, error) {
			v.mu.Lock()
			defer v.mu.Unlock()
			v.requests = append(v.requests, req)
			if req.Op == sandboxproto.OpExec {
				return sandboxproto.ExecResult{ExitCode: v.exitCode}, nil
			}
			return struct{}{}, nil
		})
	}()
	return host, nil
}

func (v *browserGuestVMM) ops() []sandboxproto.Request {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.requests)
}

func browserWake(instance string) SandboxWakeRequest {
	return SandboxWakeRequest{Instance: instance, SandboxID: "sbx-" + instance, AccountID: "acct", Plan: api.PlanPro,
		BaseKey: "base/runner-agent-amd64.ext4", LayerKey: "base/sandbox-layer-v1-amd64.ext4", VcpuCount: 1, MemSizeMiB: 1024,
		CPUMillicores: 1000, Network: api.SandboxNetworkNone, Browser: true}
}

func TestBrowserSessionStartsTheBrowserAfterAColdBoot(t *testing.T) {
	vmm := &browserGuestVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(&fakeInputRunner{}, vmm)
	if _, err := m.WakeSandbox(t.Context(), browserWake("sbx-browser")); err != nil {
		t.Fatalf("WakeSandbox: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "sbx-browser") })
	var starts []sandboxproto.Request
	for _, r := range vmm.ops() {
		if r.Op == sandboxproto.OpTemplateStart {
			starts = append(starts, r)
		}
	}
	if len(starts) != 1 || starts[0].StartCmd != sandboxBrowserStartCmd || starts[0].ReadyCmd != sandboxBrowserReadyCmd ||
		starts[0].TimeoutMillis != sandboxBrowserReadyTimeout.Milliseconds() {
		t.Fatalf("template starts %+v; want the browser's commands once", starts)
	}
}

func TestBrowserSessionRefusesCommandsAndImages(t *testing.T) {
	m := NewManager(&fakeRunner{}, &browserGuestVMM{fakeVMM: &fakeVMM{}}, Paths{}, "1.0.0", nil, nil)
	for name, edit := range map[string]func(*SandboxWakeRequest){
		"an image":     func(r *SandboxWakeRequest) { r.ImageKey = "jobs/j.ext4" },
		"its commands": func(r *SandboxWakeRequest) { r.ImageKey, r.StartCmd = "jobs/j.ext4", "serve" },
	} {
		req := browserWake("sbx-refused")
		edit(&req)
		if _, err := m.WakeSandbox(t.Context(), req); err == nil || !strings.Contains(err.Error(), "browser") {
			t.Errorf("%s: WakeSandbox = %v, want a refusal naming the browser", name, err)
		}
	}
}

func TestBrowserPrewarmRunsAPlainExec(t *testing.T) {
	vmm := &browserGuestVMM{fakeVMM: &fakeVMM{}}
	if err := prewarmSandboxBrowser(t.Context(), vmm, "sbx-capture"); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	ops := vmm.ops()
	if len(ops) != 1 || ops[0].Op != sandboxproto.OpExec || !slices.Equal(ops[0].Argv, []string{"gregale-browser", "--prewarm"}) {
		t.Fatalf("prewarm ran %+v; want one exec of gregale-browser --prewarm and no template_start", ops)
	}
	vmm.exitCode = 1
	if err := prewarmSandboxBrowser(t.Context(), vmm, "sbx-capture"); err == nil {
		t.Fatal("a failed prewarm was accepted")
	}
}
