package vmmdgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
)

// AgentRunVMMAPI is the agent run capability (ADR-20261027090010 §6). *fcvm.Manager
// implements it.
type AgentRunVMMAPI interface {
	WakeAgentRun(context.Context, fcvm.AgentRunWakeRequest) (*fcvm.Instance, error)
	RunAgent(ctx context.Context, instance string, req agentrunproto.Request, receive agentrunproto.OutputReceiver, cancel <-chan struct{}) (agentrunproto.Result, error)
	Destroy(context.Context, string) error
}

// WithAgentRuns turns the RunAgent RPC on. It is off unless vmmd's
// agent_runs switch is set (ADR-20261027090010 §1).
func (s *Server) WithAgentRuns(enabled bool) *Server {
	s.agentRunsEnabled = enabled
	return s
}

// RunAgent boots one agent run VM, sends it the run, and streams its output.
// The VM is destroyed before the result frame, and when the stream ends early.
func (s *Server) RunAgent(stream vmmdpb.Vmmd_RunAgentServer) error {
	const op = "RunAgent"
	started := time.Now()
	var opErr error
	defer func() { s.ops.Observe(op, time.Since(started), opErr) }()
	agentVMM, ok := s.vmm.(AgentRunVMMAPI)
	if !s.agentRunsEnabled || !ok {
		problem := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented, "Agent runs unavailable", "vmmd agent runs are not enabled")
		opErr = problem
		return grpcerr.ToStatus(problem)
	}
	first, err := stream.Recv()
	if err != nil {
		opErr = err
		return err
	}
	wake, run, problem := agentRunStartFromProto(first.GetStart())
	if problem != nil {
		opErr = problem
		return grpcerr.ToStatus(problem)
	}
	ctx := stream.Context()
	inst, err := agentVMM.WakeAgentRun(ctx, wake)
	if err != nil {
		opErr = err
		return grpcerr.ToStatus(toProblem(err))
	}
	if err := stream.Send(&vmmdpb.RunAgentEvent{Frame: &vmmdpb.RunAgentEvent_Booted{Booted: &vmmdpb.RunAgentBooted{
		Method: wakeMethodFrom(inst.Method), BootMs: time.Since(started).Milliseconds(),
	}}}); err != nil {
		destroyAgentRun(ctx, agentVMM, wake.Instance)
		opErr = err
		return err
	}
	result, err := agentVMM.RunAgent(ctx, wake.Instance, run, agentRunOutputSender(stream), watchAgentRunCancel(stream))
	if err != nil {
		opErr = err
		return grpcerr.ToStatus(api.NewProblem(int(codes.Unavailable), api.CodeInternal, "Agent run failed", "the agent run exchange did not complete"))
	}
	body, err := json.Marshal(result)
	if err != nil {
		opErr = err
		return grpcerr.ToStatus(api.NewProblem(int(codes.Internal), api.CodeInternal, "Agent run failed", "vmmd could not encode the result"))
	}
	if err := stream.Send(&vmmdpb.RunAgentEvent{Frame: &vmmdpb.RunAgentEvent_Result{Result: &vmmdpb.RunAgentResult{Result: body}}}); err != nil {
		opErr = err
		return err
	}
	return nil
}

func agentRunStartFromProto(start *vmmdpb.RunAgentStart) (fcvm.AgentRunWakeRequest, agentrunproto.Request, *api.Problem) {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(int(codes.InvalidArgument), api.CodeValidation, "Invalid agent run", detail)
	}
	if start == nil {
		return fcvm.AgentRunWakeRequest{}, agentrunproto.Request{}, invalid("the first frame must be start")
	}
	wake := fcvm.AgentRunWakeRequest{
		Instance: start.GetInstance(), AccountID: start.GetAccountId(), Plan: api.Plan(start.GetPlan()),
		BaseKey: start.GetBaseKey(), LayerKey: start.GetLayerKey(), VcpuCount: int(start.GetVcpuCount()),
		MemSizeMiB: int(start.GetMemSizeMib()), CPUMillicores: int(start.GetCpuMillicores()),
	}
	if wake.Instance == "" || wake.AccountID == "" || !wake.Plan.Valid() || wake.BaseKey == "" || wake.LayerKey == "" ||
		wake.VcpuCount < 1 || wake.MemSizeMiB < 1 || wake.CPUMillicores < 1 {
		return fcvm.AgentRunWakeRequest{}, agentrunproto.Request{}, invalid("instance, account, plan, base, layer and a positive machine shape are required")
	}
	var run agentrunproto.Request
	if err := json.Unmarshal(start.GetRun(), &run); err != nil {
		return fcvm.AgentRunWakeRequest{}, agentrunproto.Request{}, invalid("run is not an agent run request")
	}
	if err := run.Validate(); err != nil {
		return fcvm.AgentRunWakeRequest{}, agentrunproto.Request{}, invalid("run fails the guest-boundary checks")
	}
	if snap := start.GetSnapshot(); snap != nil {
		if snap.GetStorageKey() == "" || snap.GetVmstateStorageKey() == "" || snap.GetFcVersion() == "" || snap.GetMemBytes() < 1 {
			return fcvm.AgentRunWakeRequest{}, agentrunproto.Request{}, invalid("snapshot needs its memory and state keys, version and size")
		}
		wake.Snapshot = &fcvm.Snapshot{StorageKey: snap.GetStorageKey(), VMStateStorageKey: snap.GetVmstateStorageKey(),
			FCVersion: snap.GetFcVersion(), MemBytes: snap.GetMemBytes(), Networkless: true}
	}
	if key := start.GetInputKey(); key != "" || run.Checkout != nil {
		if !agentrunproto.ValidInputKey(key, wake.AccountID) || run.Checkout == nil {
			return fcvm.AgentRunWakeRequest{}, agentrunproto.Request{}, invalid("a checkout and its input drive key go together")
		}
		wake.InputKey = key
	}
	if relay := start.GetRelay(); relay != nil {
		wake.Relay = &fcvm.AgentRunRelay{RunID: relay.GetRunId(), LeaseToken: relay.GetLeaseToken(), TenantID: relay.GetTenantId(),
			AgentID: relay.GetAgentId(), AgentVersion: int(relay.GetAgentVersion())}
		if !wake.Relay.Valid() || wake.Relay.RunID != run.RunID {
			return fcvm.AgentRunWakeRequest{}, agentrunproto.Request{}, invalid("relay identity is malformed or names another run")
		}
	}
	return wake, run, nil
}

// agentRunOutputSender forwards guest output; the protocol reader calls it
// from one goroutine, so Sends never overlap.
func agentRunOutputSender(stream vmmdpb.Vmmd_RunAgentServer) agentrunproto.OutputReceiver {
	return func(ctx context.Context, outputStream string, chunk []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return stream.Send(&vmmdpb.RunAgentEvent{Frame: &vmmdpb.RunAgentEvent_Output{
			Output: &vmmdpb.ExecuteExecutionOutputChunk{Stream: outputStream, Chunk: append([]byte(nil), chunk...)},
		}})
	}
}

// watchAgentRunCancel closes the returned channel on the client's cancel
// frame. Any other later frame, or the client half-closing, is ignored; the
// stream's context ends the run if the client goes away.
func watchAgentRunCancel(stream vmmdpb.Vmmd_RunAgentServer) <-chan struct{} {
	cancel := make(chan struct{})
	var once sync.Once
	go func() {
		for {
			frame, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					once.Do(func() { close(cancel) })
				}
				return
			}
			if frame.GetCancel() != nil {
				once.Do(func() { close(cancel) })
			}
		}
	}()
	return cancel
}

func destroyAgentRun(ctx context.Context, vmm AgentRunVMMAPI, instance string) {
	destroyCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	_ = vmm.Destroy(destroyCtx, instance)
}

// AgentRunCaptureAPI is the optional VMM surface for preset snapshots.
type AgentRunCaptureAPI interface {
	CaptureAgentRunSnapshot(ctx context.Context, c fcvm.AgentRunCapture) (*fcvm.Snapshot, fcvm.SnapshotInfo, error)
}

// agentRunCaptureAccount owns capture VMs: platform machinery, no tenant.
const agentRunCaptureAccount = "00000000-0000-0000-0000-000000000000"

// CaptureAgentSnapshot captures a preset snapshot for one machine shape
// (ADR-20261027090010 §2). Like RunAgent it is refused unless agent runs are on.
func (s *Server) CaptureAgentSnapshot(ctx context.Context, req *vmmdpb.CaptureAgentSnapshotRequest) (*vmmdpb.CaptureAgentSnapshotResponse, error) {
	const op = "CaptureAgentSnapshot"
	started := time.Now()
	var opErr error
	defer func() { s.ops.Observe(op, time.Since(started), opErr) }()
	capturer, ok := s.vmm.(AgentRunCaptureAPI)
	if !s.agentRunsEnabled || !ok {
		problem := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented, "Agent runs unavailable", "vmmd agent runs are not enabled")
		opErr = problem
		return nil, grpcerr.ToStatus(problem)
	}
	account := req.GetAccountId()
	if account == "" {
		account = agentRunCaptureAccount
	}
	c := fcvm.AgentRunCapture{Namespace: req.GetNamespace(), CaptureID: req.GetCaptureId(), Wake: fcvm.AgentRunWakeRequest{
		Instance: req.GetInstance(), AccountID: account, Plan: api.Plan(req.GetPlan()), BaseKey: req.GetBaseKey(),
		LayerKey: req.GetLayerKey(), VcpuCount: int(req.GetVcpuCount()), MemSizeMiB: int(req.GetMemSizeMib()),
		CPUMillicores: int(req.GetCpuMillicores()),
	}}
	snap, info, err := capturer.CaptureAgentRunSnapshot(ctx, c)
	if err != nil {
		opErr = err
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.CaptureAgentSnapshotResponse{VmstateBytes: info.VMStateBytes, Snapshot: &vmmdpb.AgentRunSnapshot{
		StorageKey: snap.StorageKey, VmstateStorageKey: snap.VMStateStorageKey, FcVersion: snap.FCVersion, MemBytes: snap.MemBytes,
	}}, nil
}
