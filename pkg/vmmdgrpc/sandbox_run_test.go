// adr: 20261027090013

package vmmdgrpc_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// sandboxRunVMM boots sbx-vm for sbx-1 and freezes and thaws it in place.
type sandboxRunVMM struct {
	*sandboxVMM
	mu        sync.Mutex
	woken     []fcvm.SandboxWakeRequest
	destroyed []string
	frozen    bool
	thaws     int
	busy      bool
}

func newSandboxRunVMM() *sandboxRunVMM {
	return &sandboxRunVMM{sandboxVMM: &sandboxVMM{fakeVMM: &fakeVMM{}}}
}

func (v *sandboxRunVMM) WakeSandbox(_ context.Context, req fcvm.SandboxWakeRequest) (*fcvm.Instance, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.woken = append(v.woken, req)
	return &fcvm.Instance{SandboxID: req.SandboxID, ExecutionOnly: true, Method: fcvm.WakeColdBoot}, nil
}

func (v *sandboxRunVMM) Destroy(_ context.Context, instance string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.destroyed = append(v.destroyed, instance)
	return nil
}

func (v *sandboxRunVMM) Freeze(_ context.Context, _ string, _ fcvm.FreezeOptions) (fcvm.FreezeReport, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.busy {
		return fcvm.FreezeReport{}, fcvm.ErrInstanceBusy
	}
	v.frozen = true
	return fcvm.FreezeReport{}, nil
}

func (v *sandboxRunVMM) ThawWith(context.Context, string, fcvm.ThawOptions) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.frozen = false
	v.thaws++
	return nil
}

func (v *sandboxRunVMM) AwaitFrozen(context.Context, string) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.frozen, nil
}

func (v *sandboxRunVMM) state() (woken int, destroyed []string, frozen bool, thaws int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.woken), append([]string(nil), v.destroyed...), v.frozen, v.thaws
}

func newSandboxRunClient(t *testing.T, vmm vmmdgrpc.VmmdAPI, enabled bool) vmmdpb.VmmdClient {
	t.Helper()
	srv := grpc.NewServer()
	vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_sandbox_run_test"), "1.0.0", nil).WithAgentRuns(enabled).Register(srv)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

func sandboxRunStart() *vmmdpb.RunSandboxRequest {
	return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Start{Start: &vmmdpb.RunSandboxStart{
		Instance: "sbx-vm", SandboxId: "sbx-1", AccountId: "acct-1", Plan: "pro", BaseKey: "base/runner-agent-amd64.ext4",
		LayerKey: "base/sandbox-layer-v1-amd64.ext4", VcpuCount: 1, MemSizeMib: 1024, CpuMillicores: 1000, QuietPauseAfterMs: 2000,
	}}}
}

// bootSandbox opens a RunSandbox stream and reads its booted frame.
func bootSandbox(t *testing.T, cli vmmdpb.VmmdClient) vmmdpb.Vmmd_RunSandboxClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := cli.RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(sandboxRunStart()); err != nil {
		t.Fatal(err)
	}
	ev, err := stream.Recv()
	if err != nil || ev.GetBooted() == nil {
		t.Fatalf("first event = %v, %v; want booted", ev, err)
	}
	return stream
}

// recvUntil reads events until want matches one.
func recvUntil(t *testing.T, stream vmmdpb.Vmmd_RunSandboxClient, want func(*vmmdpb.RunSandboxEvent) bool) *vmmdpb.RunSandboxEvent {
	t.Helper()
	for {
		ev, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if want(ev) {
			return ev
		}
	}
}

func runSandboxCall(t *testing.T, cli vmmdpb.VmmdClient) {
	t.Helper()
	frames, err := sandboxCall(t, cli, sandboxStart(t, "sbx-1", sandboxproto.Request{Op: sandboxproto.OpExec, Argv: []string{"true"},
		TimeoutMillis: 1000, MaxOutputBytes: 1024}))
	if err != nil || len(frames) == 0 || frames[len(frames)-1].GetType() != sandboxproto.FrameResult {
		t.Fatalf("call = %v, %v", frames, err)
	}
}

func TestRunSandboxReportsCallsAndDestroysOnClose(t *testing.T) {
	vmm := newSandboxRunVMM()
	cli := newSandboxRunClient(t, vmm, true)
	stream := bootSandbox(t, cli)
	before := time.Now()
	runSandboxCall(t, cli)
	ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool {
		return ev.GetActivity() != nil && ev.GetActivity().GetCallsInFlight() == 0 && ev.GetActivity().GetLastCallEndedUnixMs() > 0
	})
	if ended := time.UnixMilli(ev.GetActivity().GetLastCallEndedUnixMs()); ended.Before(before.Add(-time.Second)) {
		t.Fatalf("last call ended %v, before the call", ended)
	}
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("the stream outlived close")
	}
	if woken, destroyed, _, _ := vmm.state(); woken != 1 || len(destroyed) != 1 || destroyed[0] != "sbx-vm" {
		t.Fatalf("woken %d, destroyed %v", woken, destroyed)
	}
}

func TestRunSandboxFreezesAndACallThawsInPlace(t *testing.T) {
	vmm := newSandboxRunVMM()
	cli := newSandboxRunClient(t, vmm, true)
	stream := bootSandbox(t, cli)
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Freeze{Freeze: &vmmdpb.RunSandboxFreeze{}}}); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetFrozen() != nil })
	runSandboxCall(t, cli)
	recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetThawed() != nil })
	if _, _, frozen, thaws := vmm.state(); frozen || thaws != 1 {
		t.Fatalf("frozen %v after %d thaws", frozen, thaws)
	}
	vmm.mu.Lock()
	vmm.busy = true
	vmm.mu.Unlock()
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Freeze{Freeze: &vmmdpb.RunSandboxFreeze{}}}); err != nil {
		t.Fatal(err)
	}
	ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetFreezeRefused() != nil })
	if ev.GetFreezeRefused().GetReason() != "busy" {
		t.Fatalf("refusal = %q, want busy", ev.GetFreezeRefused().GetReason())
	}
	// Ending the stream destroys the VM, as close does.
	_ = stream.CloseSend()
	if _, err := stream.Recv(); err == nil {
		t.Fatal("the stream outlived its caller")
	}
	if _, destroyed, _, _ := vmm.state(); len(destroyed) != 1 {
		t.Fatalf("destroyed %v after the caller left", destroyed)
	}
}

func TestRunSandboxRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		start   *vmmdpb.RunSandboxRequest
		want    codes.Code
	}{
		{"agent runs off", false, sandboxRunStart(), codes.Unimplemented},
		{"no start", true, &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}, codes.InvalidArgument},
		{"no sandbox", true, func() *vmmdpb.RunSandboxRequest {
			r := sandboxRunStart()
			r.GetStart().SandboxId = ""
			return r
		}(), codes.InvalidArgument},
		{"web network", true, func() *vmmdpb.RunSandboxRequest {
			r := sandboxRunStart()
			r.GetStart().NetworkMode = "web"
			return r
		}(), codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := newSandboxRunVMM()
			stream, err := newSandboxRunClient(t, vmm, tc.enabled).RunSandbox(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(tc.start); err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if woken, _, _, _ := vmm.state(); woken != 0 {
				t.Fatal("a refused start booted a VM")
			}
		})
	}
}

// A seller-image session reaches the wake with its image key; any other
// artifact named as the image is refused before a VM exists.
func TestRunSandboxTakesOnlyJobImages(t *testing.T) {
	for key, ok := range map[string]bool{
		"jobs/0b6f5c6e-3b0a-4f43-9a55-2f6c1b9d7e10.ext4":                  true,
		"apps/sbt-0123456789ab/0b6f5c6e-3b0a-4f43-9a55-2f6c1b9d7e10.ext4": true,
		"apps/acme/0b6f5c6e-3b0a-4f43-9a55-2f6c1b9d7e10.ext4":             false,
		"base/runner-agent-amd64.ext4":                                    false,
	} {
		vmm := newSandboxRunVMM()
		cli := newSandboxRunClient(t, vmm, true)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		stream, err := cli.RunSandbox(ctx)
		if err != nil {
			t.Fatal(err)
		}
		start := sandboxRunStart()
		start.GetStart().ImageKey = key
		if err := stream.Send(start); err != nil {
			t.Fatal(err)
		}
		ev, err := stream.Recv()
		switch {
		case ok && (err != nil || ev.GetBooted() == nil):
			t.Errorf("%s: first event = %v, %v; want booted", key, ev, err)
		case !ok && status.Code(err) != codes.InvalidArgument:
			t.Errorf("%s: err = %v, want InvalidArgument", key, err)
		}
		vmm.mu.Lock()
		woken := append([]fcvm.SandboxWakeRequest(nil), vmm.woken...)
		vmm.mu.Unlock()
		if ok && (len(woken) != 1 || woken[0].ImageKey != key) || !ok && len(woken) != 0 {
			t.Errorf("%s: woken %+v", key, woken)
		}
		cancel()
	}
}
