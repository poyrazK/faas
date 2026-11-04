// adr: 20261027090013
package scheddgrpc_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// fakeSandboxRunner places, boots, reports one call, then serves control
// until close.
type fakeSandboxRunner struct {
	admission sched.SandboxAdmission
	booted    chan struct{}
	err       error
	// persist asks for a workspace persist after booting; recorded is
	// agentd's record of it.
	persist  bool
	recorded chan sched.SandboxPersistRecorded
}

func (f *fakeSandboxRunner) RunSandbox(ctx context.Context, a sched.SandboxAdmission, placed func(sched.AgentRunPlacement) error, hooks sched.SandboxHooks, control <-chan sched.SandboxControl) error {
	f.admission = a
	if f.err != nil {
		return f.err
	}
	if err := placed(sched.AgentRunPlacement{Instance: sched.SandboxInstanceID(a.SandboxID), NodeID: "agents-1"}); err != nil {
		return err
	}
	close(f.booted)
	_ = hooks.Booted(ctx, "WAKE_COLD_BOOT", 700)
	_ = hooks.Activity(ctx, sched.SandboxActivity{LastCallEnded: time.UnixMilli(1_800_000_000_000)})
	if f.persist {
		_ = hooks.PersistRequested(ctx, "req-1")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req := <-control:
			switch req.Op {
			case sched.SandboxOpClose:
				return nil
			case sched.SandboxOpPersistGrant:
				g := req.PersistGrant
				_ = hooks.PersistDone(ctx, sched.SandboxPersistDone{RequestID: g.RequestID, SnapshotID: g.SnapshotID, Bytes: g.MaxBytes - 1, SHA256: g.StorageKey})
			case sched.SandboxOpPersistRecorded:
				f.recorded <- *req.PersistRecorded
			default:
				_ = hooks.Frozen(ctx)
				_ = hooks.Thawed(ctx)
			}
		}
	}
}

func sandboxScheddClient(t *testing.T, runner scheddgrpc.SandboxRunner) scheddpb.ScheddClient {
	t.Helper()
	srv := grpc.NewServer()
	impl := scheddgrpc.New(&fakeEngine{}, wire.NewOpsMetrics("schedd_sandbox_test"), nil)
	if runner != nil {
		impl.WithSandboxRunner(runner)
	}
	impl.Register(srv)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return scheddpb.NewScheddClient(conn)
}

func sandboxRunStart() *scheddpb.SandboxRunRequest {
	return &scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Start{Start: &scheddpb.SandboxRunStart{
		SandboxId: "11111111-2222-3333-4444-555555555555", AccountId: "acct", Plan: string(api.PlanPro), RamMb: 1024, Vcpu: 1, CpuMillicores: 1000}}}
}

func TestRunSandboxWaitsForProceedAndRelays(t *testing.T) {
	runner := &fakeSandboxRunner{booted: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := sandboxScheddClient(t, runner).RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(sandboxRunStart()); err != nil {
		t.Fatal(err)
	}
	ev, err := stream.Recv()
	if err != nil || ev.GetPlaced().GetNodeId() != "agents-1" {
		t.Fatalf("first event = %v, %v; want placed", ev, err)
	}
	select {
	case <-runner.booted:
		t.Fatal("the session booted before proceed")
	case <-time.After(50 * time.Millisecond):
	}
	send := func(r *scheddpb.SandboxRunRequest) {
		t.Helper()
		if err := stream.Send(r); err != nil {
			t.Fatal(err)
		}
	}
	send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Proceed{Proceed: &scheddpb.AgentRunProceed{}}})
	want := []func(*scheddpb.SandboxRunEvent) bool{
		func(e *scheddpb.SandboxRunEvent) bool { return e.GetBooted().GetBootMs() == 700 },
		func(e *scheddpb.SandboxRunEvent) bool {
			return e.GetActivity().GetLastCallEndedUnixMs() == 1_800_000_000_000
		},
	}
	for i, ok := range want {
		if ev, err := stream.Recv(); err != nil || !ok(ev) {
			t.Fatalf("event %d = %v, %v", i, ev, err)
		}
	}
	send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Freeze{Freeze: &scheddpb.SandboxRunFreeze{}}})
	if ev, err := stream.Recv(); err != nil || ev.GetFrozen() == nil {
		t.Fatalf("after freeze: %v, %v", ev, err)
	}
	if ev, err := stream.Recv(); err != nil || ev.GetThawed() == nil {
		t.Fatalf("after freeze: %v, %v; want thawed", ev, err)
	}
	send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Close{Close: &scheddpb.SandboxRunClose{}}})
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("close ended with %v, want a clean end", err)
	}
	if runner.admission.RAMMB != 1024 || runner.admission.Plan != api.PlanPro {
		t.Fatalf("admission = %+v", runner.admission)
	}
}

func TestRunSandboxStatusCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		runner scheddgrpc.SandboxRunner
		want   codes.Code
	}{
		{"off", nil, codes.Unimplemented},
		{"no capacity", &fakeSandboxRunner{err: api.NewProblem(503, api.CodeCapacity, "Full", "no node")}, codes.ResourceExhausted},
		{"outside the plan", &fakeSandboxRunner{err: &sched.AgentRunInvalidError{Reason: "x"}}, codes.FailedPrecondition},
		{"workspace unavailable", &fakeSandboxRunner{err: api.NewProblem(409, api.CodeSandboxWorkspaceUnavailable, "x", "gone")}, codes.DataLoss},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := sandboxScheddClient(t, tc.runner).RunSandbox(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(sandboxRunStart()); err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
		})
	}
}

// adr: 20261027090013 step 7 — schedd relays a workspace persist's request
// and done events up to agentd, and its grant and record down to vmmd.
func TestRunSandboxRelaysAPersist(t *testing.T) {
	runner := &fakeSandboxRunner{booted: make(chan struct{}), persist: true, recorded: make(chan sched.SandboxPersistRecorded, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := sandboxScheddClient(t, runner).RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := sandboxRunStart()
	start.GetStart().WorkspaceKey = "agents/workspaces/acct/s.tar.gz"
	for _, r := range []*scheddpb.SandboxRunRequest{start, {Frame: &scheddpb.SandboxRunRequest_Proceed{Proceed: &scheddpb.AgentRunProceed{}}}} {
		if err := stream.Send(r); err != nil {
			t.Fatal(err)
		}
	}
	next := func(ok func(*scheddpb.SandboxRunEvent) bool) *scheddpb.SandboxRunEvent {
		t.Helper()
		for {
			ev, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if ok(ev) {
				return ev
			}
		}
	}
	req := next(func(e *scheddpb.SandboxRunEvent) bool { return e.GetPersistRequested() != nil })
	if req.GetPersistRequested().GetRequestId() != "req-1" {
		t.Fatalf("requested %v", req)
	}
	if err := stream.Send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_PersistGrant{PersistGrant: &scheddpb.SandboxRunPersistGrant{
		RequestId: "req-1", SnapshotId: "snap", StorageKey: "key", MaxBytes: 10}}}); err != nil {
		t.Fatal(err)
	}
	done := next(func(e *scheddpb.SandboxRunEvent) bool { return e.GetPersistDone() != nil }).GetPersistDone()
	if done.GetRequestId() != "req-1" || done.GetSnapshotId() != "snap" || done.GetBytes() != 9 || done.GetSha256() != "key" {
		t.Fatalf("done %v", done)
	}
	if err := stream.Send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_PersistRecorded{PersistRecorded: &scheddpb.SandboxRunPersistRecorded{
		RequestId: "req-1", Recorded: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := <-runner.recorded; got.RequestID != "req-1" || !got.Recorded {
		t.Fatalf("recorded %+v", got)
	}
	if runner.admission.WorkspaceKey != "agents/workspaces/acct/s.tar.gz" {
		t.Fatalf("admission %+v", runner.admission)
	}
}
