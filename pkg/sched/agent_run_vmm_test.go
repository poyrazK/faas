// adr: 20261027090010
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// agentRunVmmd answers RunAgent as vmmd does: booted, output, then the
// result, or a cancelled result once the cancel frame arrives.
type agentRunVmmd struct {
	vmmdpb.UnimplementedVmmdServer
	start  *vmmdpb.RunAgentStart
	result agentrunproto.Result
	output string
}

func (v *agentRunVmmd) RunAgent(stream vmmdpb.Vmmd_RunAgentServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	v.start = first.GetStart()
	_ = stream.Send(&vmmdpb.RunAgentEvent{Frame: &vmmdpb.RunAgentEvent_Booted{Booted: &vmmdpb.RunAgentBooted{Method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, BootMs: 900}}})
	_ = stream.Send(&vmmdpb.RunAgentEvent{Frame: &vmmdpb.RunAgentEvent_Output{Output: &vmmdpb.ExecuteExecutionOutputChunk{Stream: "stdout", Chunk: []byte(v.output)}}})
	result := v.result
	if result.Status == agentrunproto.StatusCancelled {
		if frame, err := stream.Recv(); err != nil || frame.GetCancel() == nil {
			return errors.New("expected a cancel frame")
		}
	}
	body, _ := json.Marshal(result)
	return stream.Send(&vmmdpb.RunAgentEvent{Frame: &vmmdpb.RunAgentEvent_Result{Result: &vmmdpb.RunAgentResult{Result: body}}})
}

func (v *agentRunVmmd) CaptureAgentSnapshot(_ context.Context, req *vmmdpb.CaptureAgentSnapshotRequest) (*vmmdpb.CaptureAgentSnapshotResponse, error) {
	if req.GetNamespace() == "" || req.GetMemSizeMib() != 1024 {
		return nil, errors.New("unexpected capture request")
	}
	return &vmmdpb.CaptureAgentSnapshotResponse{Snapshot: &vmmdpb.AgentRunSnapshot{StorageKey: "snap/" + req.GetNamespace() + "/captures/" + req.GetCaptureId() + "/v2/mem",
		VmstateStorageKey: "snap/" + req.GetNamespace() + "/captures/" + req.GetCaptureId() + "/v2/vmstate", FcVersion: "1.7.0", MemBytes: 1 << 30}}, nil
}

func agentRunClient(t *testing.T, fake *agentRunVmmd) *sched.VMMClient {
	t.Helper()
	srv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(srv, fake)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return sched.NewVMMClient(conn)
}

func agentRunRequest() agentrunproto.Request {
	return agentrunproto.Request{Version: agentrunproto.Version, RunID: "run-1", Command: []string{"codex"}, TimeoutSec: 30,
		MaxLogBytes: 64, MaxResultBytes: 1024, MaxOutputFiles: 1, MaxOutputBytes: 1024}
}

func TestVMMClientRunAgent(t *testing.T) {
	fake := &agentRunVmmd{output: "working", result: agentrunproto.Result{Status: agentrunproto.StatusSucceeded, Result: json.RawMessage(`{"ok":true}`)}}
	cli := agentRunClient(t, fake)
	spec := sched.AgentRunSpec{Instance: "agent-1", AccountID: "acct", Plan: api.PlanPro, BaseKey: "b", LayerKey: "l", VCPU: 1, MemSizeMiB: 1024, CPUMillicores: 1000,
		Relay:    &sched.AgentRunRelay{RunID: "run-1", LeaseToken: "lease-1", TenantID: "tenant-1", AgentID: "agent-1", AgentVersion: 4},
		InputKey: "agents/inputs/images/acct/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.img"}
	var booted string
	var streamed string
	result, err := cli.RunAgent(context.Background(), spec, agentRunRequest(), sched.AgentRunHooks{
		Booted: func(_ context.Context, method string, _ int64) error { booted = method; return nil },
		Output: func(_ context.Context, _ string, chunk []byte) error { streamed += string(chunk); return nil },
	}, nil)
	if err != nil || result.Status != agentrunproto.StatusSucceeded || string(result.Stdout) != "working" {
		t.Fatalf("result %+v %v", result, err)
	}
	if relay := fake.start.GetRelay(); relay.GetLeaseToken() != "lease-1" || relay.GetTenantId() != "tenant-1" || relay.GetAgentVersion() != 4 ||
		fake.start.GetInputKey() != "agents/inputs/images/acct/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.img" {
		t.Fatalf("relay %+v", relay)
	}
	if booted == "" || streamed != "working" || fake.start.GetInstance() != "agent-1" || fake.start.GetMemSizeMib() != 1024 {
		t.Fatalf("booted %q streamed %q start %+v", booted, streamed, fake.start)
	}
	var run agentrunproto.Request
	if err := json.Unmarshal(fake.start.GetRun(), &run); err != nil || run.RunID != "run-1" {
		t.Fatalf("run %+v %v", run, err)
	}
}

func TestVMMClientRunAgentCancelsAndBoundsLogs(t *testing.T) {
	cancel := make(chan struct{})
	close(cancel)
	fake := &agentRunVmmd{output: "x", result: agentrunproto.Result{Status: agentrunproto.StatusCancelled, ExitCode: 143}}
	result, err := agentRunClient(t, fake).RunAgent(context.Background(), sched.AgentRunSpec{Plan: api.PlanPro}, agentRunRequest(), sched.AgentRunHooks{}, cancel)
	if err != nil || result.Status != agentrunproto.StatusCancelled {
		t.Fatalf("cancel: %+v %v", result, err)
	}
	loud := &agentRunVmmd{output: string(make([]byte, 65)), result: agentrunproto.Result{Status: agentrunproto.StatusSucceeded}}
	if _, err := agentRunClient(t, loud).RunAgent(context.Background(), sched.AgentRunSpec{Plan: api.PlanPro}, agentRunRequest(), sched.AgentRunHooks{}, nil); !errors.Is(err, agentrunproto.ErrOutputLimitExceeded) {
		t.Fatalf("log budget: %v", err)
	}
}

func TestVMMClientCapturesAndRestoresSnapshots(t *testing.T) {
	fake := &agentRunVmmd{result: agentrunproto.Result{Status: agentrunproto.StatusSucceeded}}
	cli := agentRunClient(t, fake)
	spec := sched.AgentRunSpec{Instance: "agent-1", Plan: api.PlanPro, BaseKey: "b", LayerKey: "l", VCPU: 1, MemSizeMiB: 1024, CPUMillicores: 1000}
	snap, err := cli.CaptureAgentSnapshot(context.Background(), spec, "agent-preset", "abc")
	if err != nil || snap.StorageKey != "snap/agent-preset/captures/abc/v2/mem" || snap.MemBytes != 1<<30 {
		t.Fatalf("capture %+v %v", snap, err)
	}
	spec.Snapshot = snap
	if _, err := cli.RunAgent(context.Background(), spec, agentRunRequest(), sched.AgentRunHooks{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := fake.start.GetSnapshot(); got.GetVmstateStorageKey() != snap.VMStateStorageKey || got.GetFcVersion() != "1.7.0" {
		t.Fatalf("snapshot frame %+v", got)
	}
}
