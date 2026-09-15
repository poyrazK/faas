package vmmdgrpc

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ExecuteExecutionStream sends bounded guest output frames as they arrive and
// closes with one terminal response. The unary ExecuteExecution RPC remains
// the compatibility path for older schedulers.
func (s *Server) ExecuteExecutionStream(req *vmmdpb.ExecuteExecutionRequest, stream vmmdpb.Vmmd_ExecuteExecutionStreamServer) error {
	const op = "ExecuteExecutionStream"
	start := time.Now()
	var sendErr error
	defer func() { s.ops.Observe(op, time.Since(start), sendErr) }()

	executionVMM, ok := s.vmm.(ExecutionOutputVMMAPI)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Execution streaming unavailable", "vmmd execution streaming is not configured")
		sendErr = err
		return grpcerr.ToStatus(err)
	}
	wireReq, err := executionRequestFromProto(req)
	if err != nil {
		sendErr = err
		return grpcerr.ToStatus(toProblem(err))
	}

	receive := func(ctx context.Context, outputStream string, chunk []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if outputStream != "stdout" && outputStream != "stderr" {
			return status.Error(codes.Internal, "execution guest returned an invalid output stream")
		}
		return stream.Send(&vmmdpb.ExecuteExecutionEvent{Frame: &vmmdpb.ExecuteExecutionEvent_Output{
			Output: &vmmdpb.ExecuteExecutionOutputChunk{Stream: outputStream, Chunk: append([]byte(nil), chunk...)},
		}})
	}
	result, err := executionVMM.ExecuteExecutionWithOutput(stream.Context(), req.GetInstance(), wireReq, receive)
	if err != nil {
		sendErr = err
		return grpcerr.ToStatus(executionProblem(err))
	}
	if err := result.Validate(wireReq.MaxOutput); err != nil {
		sendErr = err
		return grpcerr.ToStatus(api.NewProblem(int(codes.Internal), api.CodeInternal,
			"Execution protocol failed", "vmmd received an invalid terminal result"))
	}
	terminal := executionResponseFromResult(wireReq.ExecutionID, result)
	// The stream already delivered stdout/stderr frames. Keeping the terminal
	// envelope metadata-only prevents consumers from double-counting output.
	terminal.Stdout = nil
	terminal.Stderr = nil
	if err := stream.Send(&vmmdpb.ExecuteExecutionEvent{Frame: &vmmdpb.ExecuteExecutionEvent_Terminal{
		Terminal: terminal,
	}}); err != nil {
		sendErr = err
		return err
	}
	return nil
}
