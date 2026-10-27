// adr: 20261027090010
package vmmdgrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// agentRunFake adds the agent run capability to the package's fake VMM.
type agentRunFake struct {
	*fakeVMM
	mu        sync.Mutex
	woke      []fcvm.AgentRunWakeRequest
	destroyed []string
	cancelled bool
	captures  []fcvm.AgentRunCapture
}

func (f *agentRunFake) CaptureAgentRunSnapshot(_ context.Context, c fcvm.AgentRunCapture) (*fcvm.Snapshot, fcvm.SnapshotInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captures = append(f.captures, c)
	return &fcvm.Snapshot{StorageKey: "snap/" + c.Namespace + "/captures/" + c.CaptureID + "/v2/mem",
		VMStateStorageKey: "snap/" + c.Namespace + "/captures/" + c.CaptureID + "/v2/vmstate", FCVersion: "1.7.0", MemBytes: 1 << 30,
		Networkless: true}, fcvm.SnapshotInfo{MemBytes: 1 << 30, VMStateBytes: 4096}, nil
}

func (f *agentRunFake) WakeAgentRun(_ context.Context, req fcvm.AgentRunWakeRequest) (*fcvm.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.woke = append(f.woke, req)
	return &fcvm.Instance{Lease: fcvm.Lease{Instance: req.Instance, Networkless: true}, Method: fcvm.WakeColdBoot}, nil
}

func (f *agentRunFake) RunAgent(ctx context.Context, instance string, req agentrunproto.Request, receive agentrunproto.OutputReceiver, cancel <-chan struct{}) (agentrunproto.Result, error) {
	defer func() {
		f.mu.Lock()
		f.destroyed = append(f.destroyed, instance)
		f.mu.Unlock()
	}()
	if err := receive(ctx, "stdout", []byte("ran "+req.Command[0])); err != nil {
		return agentrunproto.Result{}, err
	}
	if req.Command[0] == "wait-for-cancel" {
		select {
		case <-cancel:
			f.mu.Lock()
			f.cancelled = true
			f.mu.Unlock()
			return agentrunproto.Result{Status: agentrunproto.StatusCancelled, ExitCode: 143}, nil
		case <-time.After(5 * time.Second):
			return agentrunproto.Result{Status: agentrunproto.StatusTimedOut, ExitCode: 124}, nil
		}
	}
	return agentrunproto.Result{Status: agentrunproto.StatusSucceeded, Result: json.RawMessage(`{"ok":true}`)}, nil
}

func newAgentRunServer(t *testing.T, fake *agentRunFake, enabled bool) vmmdpb.VmmdClient {
	t.Helper()
	srv := grpc.NewServer()
	vmmdgrpc.New(fake, wire.NewOpsMetrics("vmmd_agent_test"), "1.0.0", nil).WithAgentRuns(enabled).Register(srv)
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

func agentRunStart(t *testing.T, command string) *vmmdpb.RunAgentRequest {
	t.Helper()
	run, err := json.Marshal(agentrunproto.Request{Version: agentrunproto.Version, RunID: "run-1", Command: []string{command},
		TimeoutSec: 30, MaxLogBytes: 1024, MaxResultBytes: 1024, MaxOutputFiles: 1, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return &vmmdpb.RunAgentRequest{Frame: &vmmdpb.RunAgentRequest_Start{Start: &vmmdpb.RunAgentStart{
		Instance: "agent-1", AccountId: "acct", Plan: "pro", BaseKey: "base/agent-base.ext4", LayerKey: "layer/agent.ext4",
		VcpuCount: 1, MemSizeMib: 1024, CpuMillicores: 1000, Run: run,
	}}}
}

// collect reads events until the stream ends.
func collect(t *testing.T, stream vmmdpb.Vmmd_RunAgentClient) ([]*vmmdpb.RunAgentEvent, error) {
	t.Helper()
	var events []*vmmdpb.RunAgentEvent
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		events = append(events, ev)
	}
}

func TestRunAgentStreamsBootOutputAndResult(t *testing.T) {
	fake := &agentRunFake{fakeVMM: &fakeVMM{}}
	cli := newAgentRunServer(t, fake, true)
	stream, err := cli.RunAgent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(agentRunStart(t, "codex")); err != nil {
		t.Fatal(err)
	}
	_ = stream.CloseSend()
	events, err := collect(t, stream)
	if err != nil || len(events) != 3 {
		t.Fatalf("events %v, %v", events, err)
	}
	if events[0].GetBooted() == nil || string(events[1].GetOutput().GetChunk()) != "ran codex" {
		t.Fatalf("booted/output %v", events)
	}
	var result agentrunproto.Result
	if err := json.Unmarshal(events[2].GetResult().GetResult(), &result); err != nil || result.Status != agentrunproto.StatusSucceeded {
		t.Fatalf("result %s %v", events[2].GetResult().GetResult(), err)
	}
	if len(fake.woke) != 1 || fake.woke[0].BaseKey != "base/agent-base.ext4" || fake.woke[0].MemSizeMiB != 1024 {
		t.Fatalf("wake %+v", fake.woke)
	}
}

func TestRunAgentCancelFrameReachesTheRun(t *testing.T) {
	fake := &agentRunFake{fakeVMM: &fakeVMM{}}
	cli := newAgentRunServer(t, fake, true)
	stream, err := cli.RunAgent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(agentRunStart(t, "wait-for-cancel")); err != nil {
		t.Fatal(err)
	}
	if ev, err := stream.Recv(); err != nil || ev.GetBooted() == nil {
		t.Fatalf("booted %v %v", ev, err)
	}
	if err := stream.Send(&vmmdpb.RunAgentRequest{Frame: &vmmdpb.RunAgentRequest_Cancel{Cancel: &vmmdpb.RunAgentCancel{}}}); err != nil {
		t.Fatal(err)
	}
	events, err := collect(t, stream)
	if err != nil || len(events) != 2 {
		t.Fatalf("events %v %v", events, err)
	}
	var result agentrunproto.Result
	_ = json.Unmarshal(events[1].GetResult().GetResult(), &result)
	if result.Status != agentrunproto.StatusCancelled || !fake.cancelled {
		t.Fatalf("result %+v cancelled %v", result, fake.cancelled)
	}
}

func TestRunAgentRefusesWhenOffOrInvalid(t *testing.T) {
	run := func(cli vmmdpb.VmmdClient, req *vmmdpb.RunAgentRequest) codes.Code {
		stream, err := cli.RunAgent(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Send(req)
		_ = stream.CloseSend()
		_, err = collect(t, stream)
		return status.Code(err)
	}
	if code := run(newAgentRunServer(t, &agentRunFake{fakeVMM: &fakeVMM{}}, false), agentRunStart(t, "codex")); code != codes.Unimplemented {
		t.Fatalf("switch off: %v", code)
	}
	fake := &agentRunFake{fakeVMM: &fakeVMM{}}
	cli := newAgentRunServer(t, fake, true)
	noBase := agentRunStart(t, "codex")
	noBase.GetStart().BaseKey = ""
	badRun := agentRunStart(t, "codex")
	badRun.GetStart().Run = []byte(`{"version":1,"run_id":"r","command":[]}`)
	cancelFirst := &vmmdpb.RunAgentRequest{Frame: &vmmdpb.RunAgentRequest_Cancel{Cancel: &vmmdpb.RunAgentCancel{}}}
	for name, req := range map[string]*vmmdpb.RunAgentRequest{"no base": noBase, "bad run": badRun, "cancel first": cancelFirst} {
		if code := run(cli, req); code != codes.InvalidArgument {
			t.Errorf("%s: %v", name, code)
		}
	}
	if len(fake.woke) != 0 {
		t.Fatalf("an invalid start booted a VM: %+v", fake.woke)
	}
}

// The relay principal travels with the wake, and must name the run it rides.
func TestRunAgentBindsTheRelay(t *testing.T) {
	const runID = "11111111-2222-3333-4444-555555555555"
	start := func(relayRun string) *vmmdpb.RunAgentRequest {
		req := agentRunStart(t, "codex")
		run, err := json.Marshal(agentrunproto.Request{Version: agentrunproto.Version, RunID: runID, Command: []string{"codex"},
			TimeoutSec: 30, MaxLogBytes: 1024, MaxResultBytes: 1024, MaxOutputFiles: 1, MaxOutputBytes: 1024})
		if err != nil {
			t.Fatal(err)
		}
		req.GetStart().Run = run
		req.GetStart().Relay = &vmmdpb.RunAgentRelay{RunId: relayRun, LeaseToken: "66666666-2222-3333-4444-555555555555",
			AgentId: "88888888-2222-3333-4444-555555555555", AgentVersion: 1}
		return req
	}
	fake := &agentRunFake{fakeVMM: &fakeVMM{}}
	cli := newAgentRunServer(t, fake, true)
	for name, tc := range map[string]struct {
		req  *vmmdpb.RunAgentRequest
		want codes.Code
	}{
		"bound":     {start(runID), codes.OK},
		"other run": {start("99999999-2222-3333-4444-555555555555"), codes.InvalidArgument},
	} {
		stream, err := cli.RunAgent(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Send(tc.req)
		_ = stream.CloseSend()
		if _, err := collect(t, stream); status.Code(err) != tc.want {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if len(fake.woke) != 1 || fake.woke[0].Relay == nil || fake.woke[0].Relay.RunID != runID || fake.woke[0].Relay.AgentVersion != 1 {
		t.Fatalf("wake %+v", fake.woke)
	}
}

func TestRunAgentAttachesTheInputDrive(t *testing.T) {
	start := func(key string, checkout bool) *vmmdpb.RunAgentRequest {
		req := agentRunStart(t, "codex")
		run := agentrunproto.Request{Version: agentrunproto.Version, RunID: "run-1", Command: []string{"codex"},
			TimeoutSec: 30, MaxLogBytes: 1024, MaxResultBytes: 1024, MaxOutputFiles: 1, MaxOutputBytes: 1024}
		if checkout {
			run.Checkout = &agentrunproto.Checkout{SHA256: strings.Repeat("a", 64), Bytes: 10, MaxExtractedBytes: 100, MaxEntries: 10}
		}
		body, err := json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		req.GetStart().Run, req.GetStart().InputKey = body, key
		return req
	}
	good := agentrunproto.InputImageKey("acct", strings.Repeat("b", 64))
	fake := &agentRunFake{fakeVMM: &fakeVMM{}}
	cli := newAgentRunServer(t, fake, true)
	for name, tc := range map[string]struct {
		req  *vmmdpb.RunAgentRequest
		want codes.Code
	}{
		"attached":         {start(good, true), codes.OK},
		"checkout, no key": {start("", true), codes.InvalidArgument},
		"another artifact": {start("layer/app.ext4", true), codes.InvalidArgument},
		"another account":  {start(agentrunproto.InputImageKey("other", strings.Repeat("b", 64)), true), codes.InvalidArgument},
	} {
		stream, err := cli.RunAgent(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Send(tc.req)
		_ = stream.CloseSend()
		if _, err := collect(t, stream); status.Code(err) != tc.want {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if len(fake.woke) != 1 || fake.woke[0].InputKey != good {
		t.Fatalf("wake %+v", fake.woke)
	}
}

func TestRunAgentRestoresASnapshot(t *testing.T) {
	fake := &agentRunFake{fakeVMM: &fakeVMM{}}
	cli := newAgentRunServer(t, fake, true)
	send := func(snap *vmmdpb.AgentRunSnapshot) codes.Code {
		req := agentRunStart(t, "codex")
		req.GetStart().Snapshot = snap
		stream, err := cli.RunAgent(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Send(req)
		_ = stream.CloseSend()
		_, err = collect(t, stream)
		return status.Code(err)
	}
	full := &vmmdpb.AgentRunSnapshot{StorageKey: "snap/p/captures/c/v2/mem", VmstateStorageKey: "snap/p/captures/c/v2/vmstate", FcVersion: "1.7.0", MemBytes: 1 << 30}
	if code := send(full); code != codes.OK {
		t.Fatalf("restore: %v", code)
	}
	if code := send(&vmmdpb.AgentRunSnapshot{StorageKey: "snap/p/captures/c/v2/mem"}); code != codes.InvalidArgument {
		t.Fatalf("incomplete snapshot: %v", code)
	}
	if len(fake.woke) != 1 || fake.woke[0].Snapshot == nil || !fake.woke[0].Snapshot.Networkless || fake.woke[0].Snapshot.MemBytes != 1<<30 {
		t.Fatalf("wake %+v", fake.woke)
	}
}

func TestCaptureAgentSnapshot(t *testing.T) {
	req := &vmmdpb.CaptureAgentSnapshotRequest{Instance: "agent-x", Plan: "pro", BaseKey: "base", LayerKey: "layer", VcpuCount: 1,
		MemSizeMib: 1024, CpuMillicores: 1000, Namespace: "agent-preset", CaptureId: "abc"}
	if _, err := newAgentRunServer(t, &agentRunFake{fakeVMM: &fakeVMM{}}, false).CaptureAgentSnapshot(context.Background(), req); status.Code(err) != codes.Unimplemented {
		t.Fatalf("switch off: %v", err)
	}
	fake := &agentRunFake{fakeVMM: &fakeVMM{}}
	resp, err := newAgentRunServer(t, fake, true).CaptureAgentSnapshot(context.Background(), req)
	if err != nil || resp.GetSnapshot().GetStorageKey() != "snap/agent-preset/captures/abc/v2/mem" || resp.GetSnapshot().GetFcVersion() != "1.7.0" {
		t.Fatalf("capture %+v %v", resp, err)
	}
	if len(fake.captures) != 1 || fake.captures[0].Wake.AccountID == "" || fake.captures[0].Wake.MemSizeMiB != 1024 {
		t.Fatalf("capture request %+v", fake.captures)
	}
}
