package vmmdgrpc

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type runtimeScanMaterializer interface {
	MaterializeRuntimeScan(context.Context, runtimescan.Request) (runtimescan.Receipt, error)
}

func (s *Server) MaterializeRuntimeScan(ctx context.Context, req *vmmdpb.MaterializeRuntimeScanRequest) (*vmmdpb.MaterializeRuntimeScanResponse, error) {
	request, err := runtimescan.RequestFromProto(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid runtime scan request")
	}
	owner, ok := s.vmm.(runtimeScanMaterializer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "native runtime scanner view unavailable")
	}
	start := time.Now()
	receipt, err := owner.MaterializeRuntimeScan(ctx, request)
	s.ops.Observe("MaterializeRuntimeScan", time.Since(start), err)
	if errors.Is(err, vmmdmount.ErrMountCapacity) {
		return nil, status.Error(codes.ResourceExhausted, "runtime scan mount capacity exhausted")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, status.Error(codes.DeadlineExceeded, "runtime scan deadline exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return nil, status.Error(codes.Canceled, "runtime scan canceled")
	}
	if err != nil || receipt.Check(request) != nil || ctx.Err() != nil {
		return nil, status.Error(codes.FailedPrecondition, "runtime scan materialization refused")
	}
	return receipt.ToProto(), nil
}
