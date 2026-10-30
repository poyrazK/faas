package scheddgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentRunRunner is the sched.AgentRunner surface RunAgent uses.
type AgentRunRunner interface {
	Run(ctx context.Context, a sched.AgentRunAdmission, run agentrunproto.Request, placed func(sched.AgentRunPlacement) error, hooks sched.AgentRunHooks, cancel <-chan struct{}) (agentrunproto.Result, error)
}

// WithAgentRunner serves RunAgent (ADR-20261027090010 §6). Without it, or with a nil
// runner, RunAgent answers Unimplemented.
func (s *Server) WithAgentRunner(runner AgentRunRunner) *Server {
	s.agentRuns = runner
	return s
}

// RunAgent admits one agent run on the agent pool and streams it. A placed
// run boots only after agentd's proceed frame; an error before proceed means
// nothing booted, so agentd may queue the run again.
func (s *Server) RunAgent(stream scheddpb.Schedd_RunAgentServer) error {
	const op = "RunAgent"
	started := time.Now()
	var opErr error
	defer func() { s.ops.Observe(op, time.Since(started), opErr) }()
	if s.agentRuns == nil {
		opErr = status.Error(codes.Unimplemented, "schedd agent runs are not enabled")
		return opErr
	}
	first, err := stream.Recv()
	if err != nil {
		opErr = err
		return err
	}
	admission, run, err := agentRunStartFromProto(first.GetStart())
	if err != nil {
		opErr = err
		return err
	}
	ctx := stream.Context()
	var sendMu sync.Mutex
	send := func(ev *scheddpb.AgentRunEvent) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(ev)
	}
	frames := watchAgentRunFrames(stream)
	placed := func(p sched.AgentRunPlacement) error {
		if err := send(&scheddpb.AgentRunEvent{Frame: &scheddpb.AgentRunEvent_Placed{Placed: &scheddpb.AgentRunPlaced{Instance: p.Instance, NodeId: p.NodeID}}}); err != nil {
			return err
		}
		// Nothing boots until agentd has recorded the run as running.
		select {
		case <-frames.proceed:
			return nil
		case <-frames.cancel:
			return errAgentRunNotProceeded
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	hooks := sched.AgentRunHooks{
		Booted: func(_ context.Context, method string, bootMs int64) error {
			return send(&scheddpb.AgentRunEvent{Frame: &scheddpb.AgentRunEvent_Booted{Booted: &scheddpb.AgentRunBooted{Method: method, BootMs: bootMs}}})
		},
		Output: func(_ context.Context, name string, chunk []byte) error {
			return send(&scheddpb.AgentRunEvent{Frame: &scheddpb.AgentRunEvent_Output{Output: &scheddpb.AgentRunOutput{Stream: name, Chunk: append([]byte(nil), chunk...)}}})
		},
	}
	result, err := s.agentRuns.Run(ctx, admission, run, placed, hooks, frames.cancel)
	if err != nil {
		opErr = err
		return agentRunStatus(err)
	}
	body, err := json.Marshal(result)
	if err != nil {
		opErr = err
		return status.Error(codes.Internal, "schedd could not encode the agent run result")
	}
	opErr = send(&scheddpb.AgentRunEvent{Frame: &scheddpb.AgentRunEvent_Result{Result: &scheddpb.AgentRunResult{Result: body}}})
	return opErr
}

func agentRunStartFromProto(start *scheddpb.AgentRunStart) (sched.AgentRunAdmission, agentrunproto.Request, error) {
	if start == nil {
		return sched.AgentRunAdmission{}, agentrunproto.Request{}, status.Error(codes.InvalidArgument, "the first frame must be start")
	}
	admission := sched.AgentRunAdmission{RunID: start.GetRunId(), AccountID: start.GetAccountId(), Plan: api.Plan(start.GetPlan()),
		RAMMB: int(start.GetRamMb()), VCPU: int(start.GetVcpu()), CPUMillicores: int(start.GetCpuMillicores())}
	var run agentrunproto.Request
	if err := json.Unmarshal(start.GetRun(), &run); err != nil {
		return sched.AgentRunAdmission{}, agentrunproto.Request{}, status.Error(codes.InvalidArgument, "run is not an agent run request")
	}
	if err := run.Validate(); err != nil {
		return sched.AgentRunAdmission{}, agentrunproto.Request{}, status.Error(codes.InvalidArgument, "run fails the guest-boundary checks")
	}
	if key := start.GetInputKey(); key != "" || run.Checkout != nil {
		if !agentrunproto.ValidInputKey(key, start.GetAccountId()) || run.Checkout == nil {
			return sched.AgentRunAdmission{}, agentrunproto.Request{}, status.Error(codes.InvalidArgument, "a checkout and its input drive key go together")
		}
		admission.InputKey = key
	}
	if relay := start.GetRelay(); relay != nil {
		admission.Relay = &sched.AgentRunRelay{RunID: start.GetRunId(), LeaseToken: relay.GetLeaseToken(), TenantID: relay.GetTenantId(),
			AgentID: relay.GetAgentId(), AgentVersion: int(relay.GetAgentVersion())}
		if !admission.Relay.Valid() {
			return sched.AgentRunAdmission{}, agentrunproto.Request{}, status.Error(codes.InvalidArgument, "relay identity is malformed")
		}
	}
	return admission, run, nil
}

// agentRunStatus maps a runner error to the status agentd branches on:
// ResourceExhausted for no capacity (queue it again), FailedPrecondition
// for a request outside the plan, and Unavailable for everything else.
func agentRunStatus(err error) error {
	var problem *api.Problem
	switch {
	case errors.As(err, &problem) && problem.Code == api.CodeCapacity:
		return status.Error(codes.ResourceExhausted, "no agent-pool node has capacity for this run")
	case errors.Is(err, errAgentRunNotProceeded):
		return status.Error(codes.Aborted, "the run was cancelled before it booted")
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	case errors.Is(err, sched.ErrAgentRunsDisabled):
		return status.Error(codes.Unimplemented, "schedd agent runs are not enabled")
	case isAgentRunAdmissionError(err):
		return status.Error(codes.FailedPrecondition, "the run is outside the account's plan")
	}
	return status.Error(codes.Unavailable, "the agent run did not complete")
}

func isAgentRunAdmissionError(err error) bool {
	var invalid *sched.AgentRunInvalidError
	return errors.As(err, &invalid)
}

// errAgentRunNotProceeded ends a placed run that agentd cancelled, or whose
// stream broke, before it sent proceed.
var errAgentRunNotProceeded = errors.New("scheddgrpc: agent run was not told to proceed")

type agentRunFrames struct {
	proceed <-chan struct{}
	cancel  <-chan struct{}
}

// watchAgentRunFrames reads agentd's frames after start: proceed lets the
// placed run boot; cancel, or the stream breaking, cancels. A clean
// half-close is neither.
func watchAgentRunFrames(stream scheddpb.Schedd_RunAgentServer) agentRunFrames {
	proceed, cancel := make(chan struct{}), make(chan struct{})
	var proceedOnce, cancelOnce sync.Once
	go func() {
		for {
			frame, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					cancelOnce.Do(func() { close(cancel) })
				}
				return
			}
			switch {
			case frame.GetProceed() != nil:
				proceedOnce.Do(func() { close(proceed) })
			case frame.GetCancel() != nil:
				cancelOnce.Do(func() { close(cancel) })
			}
		}
	}()
	return agentRunFrames{proceed: proceed, cancel: cancel}
}
