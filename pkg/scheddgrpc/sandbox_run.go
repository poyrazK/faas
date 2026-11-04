package scheddgrpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SandboxRunner is the sched.AgentRunner surface RunSandbox uses.
type SandboxRunner interface {
	RunSandbox(ctx context.Context, a sched.SandboxAdmission, placed func(sched.AgentRunPlacement) error, hooks sched.SandboxHooks, control <-chan sched.SandboxControl) error
}

// WithSandboxRunner serves RunSandbox (ADR-20261027090013 §4). Without it,
// or with a nil runner, RunSandbox answers Unimplemented.
func (s *Server) WithSandboxRunner(runner SandboxRunner) *Server {
	s.sandboxRuns = runner
	return s
}

// RunSandbox admits one sandbox session and keeps its VM for the stream's
// life. A placed session boots only after agentd's proceed frame; an error
// before proceed means nothing booted, so agentd may queue it again.
func (s *Server) RunSandbox(stream scheddpb.Schedd_RunSandboxServer) error {
	const op = "RunSandbox"
	started := time.Now()
	var opErr error
	defer func() { s.ops.Observe(op, time.Since(started), opErr) }()
	if s.sandboxRuns == nil {
		opErr = status.Error(codes.Unimplemented, "schedd sandbox sessions are not enabled")
		return opErr
	}
	first, err := stream.Recv()
	if err != nil {
		opErr = err
		return err
	}
	start := first.GetStart()
	if start == nil {
		opErr = status.Error(codes.InvalidArgument, "the first frame must be start")
		return opErr
	}
	admission := sched.SandboxAdmission{SandboxID: start.GetSandboxId(), AccountID: start.GetAccountId(), Plan: api.Plan(start.GetPlan()),
		RAMMB: int(start.GetRamMb()), VCPU: int(start.GetVcpu()), CPUMillicores: int(start.GetCpuMillicores())}
	ctx := stream.Context()
	var sendMu sync.Mutex
	send := func(ev *scheddpb.SandboxRunEvent) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(ev)
	}
	frames := watchSandboxFrames(ctx, stream)
	placed := func(p sched.AgentRunPlacement) error {
		if err := send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Placed{Placed: &scheddpb.AgentRunPlaced{Instance: p.Instance, NodeId: p.NodeID}}}); err != nil {
			return err
		}
		// Nothing boots until agentd has recorded where the session runs.
		select {
		case <-frames.proceed:
			return nil
		case <-frames.ended:
			return errAgentRunNotProceeded
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	hooks := sched.SandboxHooks{
		Booted: func(_ context.Context, method string, bootMs int64) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Booted{Booted: &scheddpb.AgentRunBooted{Method: method, BootMs: bootMs}}})
		},
		Activity: func(_ context.Context, a sched.SandboxActivity) error {
			ev := &scheddpb.SandboxRunActivity{CallsInFlight: int32(a.CallsInFlight)}
			if !a.LastCallEnded.IsZero() {
				ev.LastCallEndedUnixMs = a.LastCallEnded.UnixMilli()
			}
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Activity{Activity: ev}})
		},
		Frozen: func(context.Context) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Frozen{Frozen: &scheddpb.SandboxRunFrozen{}}})
		},
		FreezeRefused: func(_ context.Context, reason string) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_FreezeRefused{FreezeRefused: &scheddpb.SandboxRunFreezeRefused{Reason: reason}}})
		},
		Thawed: func(context.Context) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Thawed{Thawed: &scheddpb.SandboxRunThawed{}}})
		},
	}
	opErr = s.sandboxRuns.RunSandbox(ctx, admission, placed, hooks, frames.control)
	if opErr != nil {
		return agentRunStatus(opErr)
	}
	return nil
}

type sandboxFrames struct {
	proceed <-chan struct{}
	// ended closes when agentd's side of the stream ends before close.
	ended   <-chan struct{}
	control <-chan sched.SandboxControl
}

// watchSandboxFrames reads agentd's frames after start: proceed lets the
// placed session boot; freeze and close go to the runner. A stream that
// ends, cleanly or not, closes the session.
func watchSandboxFrames(ctx context.Context, stream scheddpb.Schedd_RunSandboxServer) sandboxFrames {
	proceed, ended := make(chan struct{}), make(chan struct{})
	control := make(chan sched.SandboxControl, 4)
	var proceedOnce sync.Once
	go func() {
		defer close(ended)
		for {
			frame, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					select {
					case control <- sched.SandboxClose:
					case <-ctx.Done():
					}
				}
				return
			}
			var req sched.SandboxControl
			switch {
			case frame.GetProceed() != nil:
				proceedOnce.Do(func() { close(proceed) })
				continue
			case frame.GetFreeze() != nil:
				req = sched.SandboxFreeze
			case frame.GetClose() != nil:
				req = sched.SandboxClose
			default:
				continue
			}
			select {
			case control <- req:
			case <-ctx.Done():
				return
			}
		}
	}()
	return sandboxFrames{proceed: proceed, ended: ended, control: control}
}
