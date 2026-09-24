package vmmdgrpc

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// RestoreAppTask creates a fresh app-task-only VM without accepting command
// data. The ordinary AppSpec wake envelope keeps secrets, env, and network
// policy identical to the pinned deployment.
func (s *Server) RestoreAppTask(ctx context.Context, req *vmmdpb.RestoreAppTaskRequest) (*vmmdpb.RestoreAppTaskResponse, error) {
	const op = "RestoreAppTask"
	start := time.Now()
	restoreVMM, ok := s.vmm.(AppTaskRestoreVMMAPI)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"App tasks unavailable", "vmmd app task restore is not configured")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	wakeReq, err := appTaskWakeRequestFromProto(ctx, req)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	inst, err := restoreVMM.WakeAppTask(ctx, wakeReq)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(appTaskProblem(err))
	}
	if inst == nil {
		return nil, grpcerr.ToStatus(api.NewProblem(int(codes.Internal), api.CodeInternal,
			"App task restore failed", "vmmd returned an empty instance"))
	}
	return &vmmdpb.RestoreAppTaskResponse{
		Instance: inst.Lease.Instance, LeaseUid: int32(inst.Lease.UID), Method: wakeMethodFrom(inst.Method),
	}, nil
}

// ExecuteAppTask dispatches one validated command to an app-task-only guest.
// Manager owns teardown before this method returns.
func (s *Server) ExecuteAppTask(ctx context.Context, req *vmmdpb.ExecuteAppTaskRequest) (*vmmdpb.ExecuteAppTaskResponse, error) {
	const op = "ExecuteAppTask"
	start := time.Now()
	taskVMM, ok := s.vmm.(AppTaskVMMAPI)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"App tasks unavailable", "vmmd app task execution is not configured")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	wireReq, err := appTaskRequestFromProto(req)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	result, err := taskVMM.ExecuteAppTask(ctx, req.GetInstance(), wireReq)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(appTaskProblem(err))
	}
	if err := result.Validate(wireReq.MaxOutputBytes); err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(api.NewProblem(int(codes.Internal), api.CodeInternal,
			"App task protocol failed", "vmmd received an invalid terminal result"))
	}
	s.ops.Observe(op, time.Since(start), nil)
	return appTaskResponseFromResult(wireReq.TaskID, result), nil
}

// ExecuteAppTaskStream forwards bounded output chunks followed by one
// metadata-only terminal result.
func (s *Server) ExecuteAppTaskStream(req *vmmdpb.ExecuteAppTaskRequest, stream vmmdpb.Vmmd_ExecuteAppTaskStreamServer) error {
	const op = "ExecuteAppTaskStream"
	start := time.Now()
	var observedErr error
	defer func() { s.ops.Observe(op, time.Since(start), observedErr) }()
	taskVMM, ok := s.vmm.(AppTaskOutputVMMAPI)
	if !ok {
		problem := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"App task streaming unavailable", "vmmd app task streaming is not configured")
		observedErr = problem
		return grpcerr.ToStatus(problem)
	}
	wireReq, err := appTaskRequestFromProto(req)
	if err != nil {
		observedErr = err
		return grpcerr.ToStatus(toProblem(err))
	}
	receive := func(ctx context.Context, outputStream string, chunk []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if outputStream != "stdout" && outputStream != "stderr" {
			return status.Error(codes.Internal, "app task guest returned an invalid output stream")
		}
		return stream.Send(&vmmdpb.ExecuteAppTaskEvent{Frame: &vmmdpb.ExecuteAppTaskEvent_Output{
			Output: &vmmdpb.ExecuteAppTaskOutputChunk{Stream: outputStream, Chunk: append([]byte(nil), chunk...)},
		}})
	}
	result, err := taskVMM.ExecuteAppTaskWithOutput(stream.Context(), req.GetInstance(), wireReq, receive)
	if err != nil {
		observedErr = err
		return grpcerr.ToStatus(appTaskProblem(err))
	}
	if err := result.Validate(wireReq.MaxOutputBytes); err != nil {
		observedErr = err
		return grpcerr.ToStatus(api.NewProblem(int(codes.Internal), api.CodeInternal,
			"App task protocol failed", "vmmd received an invalid terminal result"))
	}
	terminal := appTaskResponseFromResult(wireReq.TaskID, result)
	terminal.Stdout = nil
	terminal.Stderr = nil
	if err := stream.Send(&vmmdpb.ExecuteAppTaskEvent{Frame: &vmmdpb.ExecuteAppTaskEvent_Terminal{Terminal: terminal}}); err != nil {
		observedErr = err
		return err
	}
	return nil
}

func appTaskWakeRequestFromProto(ctx context.Context, req *vmmdpb.RestoreAppTaskRequest) (fcvm.AppTaskWakeRequest, error) {
	if req == nil || req.GetDeploymentId() == "" {
		return fcvm.AppTaskWakeRequest{}, api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid app task restore", "deployment_id is required")
	}
	wake, err := toColdBootRequest(ctx, &vmmdpb.CreateColdBootRequest{
		Instance: req.GetInstance(), App: req.GetApp(), Plan: req.GetPlan(), AccountId: req.GetAccountId(),
	})
	if err != nil {
		return fcvm.AppTaskWakeRequest{}, err
	}
	wake.DeploymentID = req.GetDeploymentId()
	return fcvm.AppTaskWakeRequest{WakeRequest: wake}, nil
}

func appTaskRequestFromProto(req *vmmdpb.ExecuteAppTaskRequest) (apptaskproto.Request, error) {
	if req == nil || req.GetInstance() == "" {
		return apptaskproto.Request{}, api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid app task request", "instance is required")
	}
	if req.GetVersion() > uint32(^uint16(0)) {
		return apptaskproto.Request{}, api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid app task request", "version is outside the supported range")
	}
	wireReq := apptaskproto.Request{
		Version: uint16(req.GetVersion()), TaskID: req.GetTaskId(), Command: append([]string(nil), req.GetCommand()...),
		CommandShell: req.GetCommandShell(), TimeoutSeconds: int(req.GetTimeoutSeconds()),
		MaxOutputBytes: int(req.GetMaxOutputBytes()),
	}
	if err := wireReq.Validate(); err != nil {
		return apptaskproto.Request{}, api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid app task request", "request failed guest-boundary validation")
	}
	return wireReq, nil
}

func appTaskResponseFromResult(taskID string, result apptaskproto.Result) *vmmdpb.ExecuteAppTaskResponse {
	resp := &vmmdpb.ExecuteAppTaskResponse{
		TaskId: taskID, Status: string(result.Status), OutputTruncated: result.OutputTruncated,
		FailureCode: result.FailureCode, FailureMessage: result.FailureMessage,
		Stdout: append([]byte(nil), result.Stdout...), Stderr: append([]byte(nil), result.Stderr...),
	}
	if result.ExitCode != nil {
		resp.ExitCode = wrapperspb.Int32(int32(*result.ExitCode))
	}
	return resp
}

func appTaskProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, context.Canceled):
		return api.NewProblem(int(codes.Canceled), api.CodeInternal, "App task cancelled", "app task request was cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return api.NewProblem(int(codes.DeadlineExceeded), api.CodeInternal, "App task timed out", "app task deadline elapsed")
	case errors.Is(err, apptaskproto.ErrInvalidRequest):
		return api.NewProblem(int(codes.InvalidArgument), api.CodeValidation, "Invalid app task request", "request failed guest-boundary validation")
	case errors.Is(err, fcvm.ErrAppTaskNotConfigured), errors.Is(err, fcvm.ErrAppTaskInstanceNotFound):
		return api.NewProblem(int(codes.FailedPrecondition), api.CodeConflict, "App task unavailable", "app task instance is not available")
	default:
		return api.NewProblem(int(codes.Internal), api.CodeInternal, "App task failed", "app task transport failed inside vmmd")
	}
}
