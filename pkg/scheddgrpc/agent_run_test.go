// adr: 20261027090010, 20261027090014
package scheddgrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
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

// fakeAgentRunner plays sched.AgentRunner: it places, boots, prints and
// answers, or fails with err before placing.
type fakeAgentRunner struct {
	admission sched.AgentRunAdmission
	err       error
	cancelled bool
	booted    bool
}

func (f *fakeAgentRunner) Run(ctx context.Context, a sched.AgentRunAdmission, run agentrunproto.Request, placed func(sched.AgentRunPlacement) error, hooks sched.AgentRunHooks, cancel <-chan struct{}) (agentrunproto.Result, error) {
	f.admission = a
	if f.err != nil {
		return agentrunproto.Result{}, f.err
	}
	if err := placed(sched.AgentRunPlacement{Instance: sched.AgentRunInstanceID(a.RunID), NodeID: "agents-1"}); err != nil {
		return agentrunproto.Result{}, err
	}
	f.booted = true
	_ = hooks.Booted(ctx, "WAKE_COLD_BOOT", 900)
	_ = hooks.Output(ctx, "stdout", []byte("ran "+run.Command[0]))
	if run.Command[0] == "wait-for-cancel" {
		select {
		case <-cancel:
			f.cancelled = true
			return agentrunproto.Result{Status: agentrunproto.StatusCancelled, ExitCode: 143}, nil
		case <-time.After(5 * time.Second):
		}
	}
	return agentrunproto.Result{Status: agentrunproto.StatusSucceeded}, nil
}

func agentRunScheddClient(t *testing.T, runner scheddgrpc.AgentRunRunner) scheddpb.ScheddClient {
	t.Helper()
	srv := grpc.NewServer()
	impl := scheddgrpc.New(&fakeEngine{}, wire.NewOpsMetrics("schedd_agent_test"), nil)
	if runner != nil {
		impl.WithAgentRunner(runner)
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

func agentRunStartFrame(t *testing.T, command string) *scheddpb.AgentRunRequest {
	t.Helper()
	run, err := json.Marshal(agentrunproto.Request{Version: agentrunproto.Version, RunID: "run-1", Command: []string{command},
		TimeoutSec: 30, MaxLogBytes: 1024, MaxResultBytes: 1024, MaxOutputFiles: 1, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return &scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Start{Start: &scheddpb.AgentRunStart{
		RunId: "11111111-2222-3333-4444-555555555555", AccountId: "acct", Plan: "pro", RamMb: 2048, Vcpu: 2, CpuMillicores: 2000, Run: run,
		Relay: &scheddpb.AgentRunRelay{LeaseToken: "66666666-2222-3333-4444-555555555555", TenantId: "77777777-2222-3333-4444-555555555555",
			AgentId: "88888888-2222-3333-4444-555555555555", AgentVersion: 2},
	}}}
}

// drainAgentRun reads events until the stream ends, answering placed with
// proceed as agentd does once it has recorded the run as running.
func drainAgentRun(stream scheddpb.Schedd_RunAgentClient) ([]*scheddpb.AgentRunEvent, error) {
	var events []*scheddpb.AgentRunEvent
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		if ev.GetPlaced() != nil {
			if err := stream.Send(agentRunProceedFrame); err != nil {
				return events, err
			}
		}
		events = append(events, ev)
	}
}

var agentRunProceedFrame = &scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Proceed{Proceed: &scheddpb.AgentRunProceed{}}}

func TestRunAgentRelaysPlacementBootOutputAndResult(t *testing.T) {
	runner := &fakeAgentRunner{}
	stream, err := agentRunScheddClient(t, runner).RunAgent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(agentRunStartFrame(t, "codex")); err != nil {
		t.Fatal(err)
	}
	events, err := drainAgentRun(stream)
	if err != nil || len(events) != 4 {
		t.Fatalf("events %v %v", events, err)
	}
	if events[0].GetPlaced().GetNodeId() != "agents-1" || events[1].GetBooted().GetBootMs() != 900 ||
		string(events[2].GetOutput().GetChunk()) != "ran codex" || events[3].GetResult() == nil {
		t.Fatalf("event order %v", events)
	}
	if runner.admission.RAMMB != 2048 || runner.admission.Plan != api.PlanPro || runner.admission.VCPU != 2 {
		t.Fatalf("admission %+v", runner.admission)
	}
	if relay := runner.admission.Relay; relay == nil || relay.RunID != "11111111-2222-3333-4444-555555555555" ||
		relay.LeaseToken != "66666666-2222-3333-4444-555555555555" || relay.AgentVersion != 2 {
		t.Fatalf("relay %+v", runner.admission.Relay)
	}
}

func TestRunAgentCancelFrame(t *testing.T) {
	runner := &fakeAgentRunner{}
	stream, err := agentRunScheddClient(t, runner).RunAgent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(agentRunStartFrame(t, "wait-for-cancel")); err != nil {
		t.Fatal(err)
	}
	if ev, err := stream.Recv(); err != nil || ev.GetPlaced() == nil {
		t.Fatalf("placed %v %v", ev, err)
	}
	if err := stream.Send(agentRunProceedFrame); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // booted, output
		if _, err := stream.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.Send(&scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Cancel{Cancel: &scheddpb.AgentRunCancel{}}}); err != nil {
		t.Fatal(err)
	}
	events, err := drainAgentRun(stream)
	if err != nil || len(events) != 1 || !runner.cancelled {
		t.Fatalf("events %v %v cancelled %v", events, err, runner.cancelled)
	}
}

// A placed run that agentd cancels before proceed never boots.
func TestRunAgentWaitsForProceed(t *testing.T) {
	runner := &fakeAgentRunner{}
	stream, err := agentRunScheddClient(t, runner).RunAgent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(agentRunStartFrame(t, "codex")); err != nil {
		t.Fatal(err)
	}
	if ev, err := stream.Recv(); err != nil || ev.GetPlaced() == nil {
		t.Fatalf("placed %v %v", ev, err)
	}
	if err := stream.Send(&scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Cancel{Cancel: &scheddpb.AgentRunCancel{}}}); err != nil {
		t.Fatal(err)
	}
	events, err := drainAgentRun(stream)
	if status.Code(err) != codes.Aborted || len(events) != 0 || runner.booted {
		t.Fatalf("events %v, %v, booted %v", events, err, runner.booted)
	}
}

func TestRunAgentStatusCodes(t *testing.T) {
	code := func(runner scheddgrpc.AgentRunRunner, frame *scheddpb.AgentRunRequest) codes.Code {
		stream, err := agentRunScheddClient(t, runner).RunAgent(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Send(frame)
		_, err = drainAgentRun(stream)
		return status.Code(err)
	}
	start := agentRunStartFrame(t, "codex")
	badRelay := agentRunStartFrame(t, "codex")
	badRelay.GetStart().Relay.LeaseToken = "not-a-lease"
	for name, tc := range map[string]struct {
		runner scheddgrpc.AgentRunRunner
		frame  *scheddpb.AgentRunRequest
		want   codes.Code
	}{
		"off":         {nil, start, codes.Unimplemented},
		"no capacity": {&fakeAgentRunner{err: api.ErrCapacity("full")}, start, codes.ResourceExhausted},
		"wrapped capacity": {&fakeAgentRunner{err: errors.Join(errors.New("sched: agent run admit"), api.ErrCapacity("full"))},
			start, codes.ResourceExhausted},
		"outside the plan": {&fakeAgentRunner{err: &sched.AgentRunInvalidError{Reason: "RAM"}}, start, codes.FailedPrecondition},
		"vmmd failed":      {&fakeAgentRunner{err: errors.New("vmmd gone")}, start, codes.Unavailable},
		"malformed relay":  {&fakeAgentRunner{}, badRelay, codes.InvalidArgument},
		"cancel first": {&fakeAgentRunner{}, &scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Cancel{Cancel: &scheddpb.AgentRunCancel{}}},
			codes.InvalidArgument},
	} {
		if got := code(tc.runner, tc.frame); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// A checkout and its input drive key travel together, and the key must be
// an input drive's.
func TestRunAgentInputDrive(t *testing.T) {
	frame := func(key string, checkout bool) *scheddpb.AgentRunRequest {
		start := agentRunStartFrame(t, "codex")
		var run agentrunproto.Request
		if err := json.Unmarshal(start.GetStart().GetRun(), &run); err != nil {
			t.Fatal(err)
		}
		if checkout {
			run.Checkout = &agentrunproto.Checkout{SHA256: strings.Repeat("a", 64), Bytes: 10, MaxExtractedBytes: 100, MaxEntries: 10}
		}
		body, err := json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		start.GetStart().Run, start.GetStart().InputKey = body, key
		return start
	}
	good := agentrunproto.InputImageKey("acct", strings.Repeat("b", 64))
	for name, tc := range map[string]struct {
		frame *scheddpb.AgentRunRequest
		want  codes.Code
	}{
		"both":             {frame(good, true), codes.OK},
		"no key":           {frame("", true), codes.InvalidArgument},
		"no checkout":      {frame(good, false), codes.InvalidArgument},
		"another artifact": {frame("base/agent-base.ext4", true), codes.InvalidArgument},
		"climbing key":     {frame(agentrunproto.InputKeyPrefix+"../base.ext4", true), codes.InvalidArgument},
		"another account":  {frame(agentrunproto.InputImageKey("other", strings.Repeat("b", 64)), true), codes.InvalidArgument},
	} {
		runner := &fakeAgentRunner{}
		stream, err := agentRunScheddClient(t, runner).RunAgent(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Send(tc.frame)
		if _, err := drainAgentRun(stream); status.Code(err) != tc.want {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
		if tc.want == codes.OK && runner.admission.InputKey != good {
			t.Errorf("%s: input key %q", name, runner.admission.InputKey)
		}
	}
}
