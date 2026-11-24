// adr: 20261116182615 §1–§2 — a browser session runs vmmd's own browser
// commands through template_start after a cold boot, refuses commands or an
// image of its own, and a capture prewarms Chromium with a plain exec.
package fcvm

import (
	"context"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// browserGuestVMM answers sandbox calls like guest-init and records them.
type browserGuestVMM struct {
	*fakeVMM
	mu       sync.Mutex
	requests []sandboxproto.Request
	exitCode int
	// stdout and stderr are what an exec writes.
	stdout, stderr string
}

func (v *browserGuestVMM) DialSandbox(ctx context.Context, _ string) (net.Conn, error) {
	host, guest := net.Pipe()
	go func() {
		_ = sandboxproto.Serve(context.WithoutCancel(ctx), guest, func(_ context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
			v.mu.Lock()
			defer v.mu.Unlock()
			v.requests = append(v.requests, req)
			if req.Op == sandboxproto.OpExec {
				_, _ = io.WriteString(call.Stderr(), v.stderr)
				_, _ = io.WriteString(call.Stdout(), v.stdout)
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

// adr: 20261124075005 §3b — a capture of the platform desktop drive runs
// gregale-desktop prewarm as the session's user; other image captures run
// no prewarm.
func TestCapturePrewarmsTheDesktop(t *testing.T) {
	vmm := &browserGuestVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(&fakeInputRunner{}, vmm)
	wake := browserWake("sbx-capture")
	wake.Browser, wake.ImageKey = false, storagekey.SandboxDesktopImage("sha256:"+strings.Repeat("ef", 32))
	if err := m.prewarmCapture(t.Context(), wake); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	ops := vmm.ops()
	if len(ops) != 1 || ops[0].Op != sandboxproto.OpExec || !slices.Equal(ops[0].Argv, []string{"/usr/local/bin/gregale-desktop", "prewarm"}) ||
		ops[0].RunAs == sandboxproto.RunAsRoot {
		t.Fatalf("desktop prewarm ran %+v; want one exec of gregale-desktop prewarm as the user", ops)
	}
	wake.ImageKey = "jobs/11111111-1111-4111-8111-111111111111.ext4"
	if err := m.prewarmCapture(t.Context(), wake); err != nil || len(vmm.ops()) != 1 {
		t.Fatalf("a job image capture prewarmed: %v %+v", err, vmm.ops())
	}
	vmm.exitCode = 1
	wake.ImageKey = storagekey.SandboxDesktopImage("sha256:" + strings.Repeat("ef", 32))
	if err := m.prewarmCapture(t.Context(), wake); err == nil {
		t.Fatal("a failed desktop prewarm was accepted")
	}
}

// adr: 20261116182615 §3 — a task's command is one exec in the session; its
// result keeps stdout only, at most the task's output cap.
func TestRunSandboxTaskKeepsStdoutWithinItsCap(t *testing.T) {
	vmm := &browserGuestVMM{fakeVMM: &fakeVMM{}, stdout: `{"title":"Example"}`, stderr: "chromium noise"}
	m := newTestManager(&fakeInputRunner{}, vmm)
	if _, err := m.WakeSandbox(t.Context(), browserWake("sbx-task")); err != nil {
		t.Fatalf("WakeSandbox: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "sbx-task") })
	argv := []string{"gregale-browser", "fetch", "--", "https://example.com/"}
	res, err := m.RunSandboxTask(t.Context(), "sbx-task", "sbx-sbx-task", argv, time.Minute, 1024)
	if err != nil || res.ExitCode != 0 || string(res.Output) != `{"title":"Example"}` || res.Truncated {
		t.Fatalf("task = %+v, %v", res, err)
	}
	ops := vmm.ops()
	last := ops[len(ops)-1]
	if last.Op != sandboxproto.OpExec || !slices.Equal(last.Argv, argv) || last.TimeoutMillis != time.Minute.Milliseconds() || last.MaxOutputBytes != 1024 {
		t.Fatalf("ran %+v", last)
	}
	vmm.exitCode = 2
	res, err = m.RunSandboxTask(t.Context(), "sbx-task", "sbx-sbx-task", argv, time.Minute, 5)
	if err != nil || res.ExitCode != 2 || string(res.Output) != `{"tit` || !res.Truncated {
		t.Fatalf("a capped task = %+v, %v", res, err)
	}
	if _, err := m.RunSandboxTask(t.Context(), "sbx-task", "sbx-sbx-task", nil, time.Minute, 5); err == nil {
		t.Fatal("a task without a command ran")
	}
}
