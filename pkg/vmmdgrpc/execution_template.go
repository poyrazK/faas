package vmmdgrpc

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CaptureExecutionTemplate is a schedd-only producer, never a tenant Run.
func (s *Server) CaptureExecutionTemplate(ctx context.Context, req *vmmdpb.CaptureExecutionTemplateRequest) (_ *vmmdpb.CaptureExecutionTemplateResponse, result error) {
	started := time.Now()
	defer func() { s.ops.Observe("CaptureExecutionTemplate", time.Since(started), result) }()
	capturer, ok := s.vmm.(interface {
		CaptureExecutionTemplate(context.Context, fcvm.ExecutionTemplateCapture) (*fcvm.Snapshot, fcvm.SnapshotInfo, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "execution template capture unavailable")
	}
	snap, info, err := capturer.CaptureExecutionTemplate(ctx, fcvm.ExecutionTemplateCapture{
		AccountID: req.GetAccountId(), Runtime: req.GetRuntime(), BaseKey: req.GetBaseKey(), LayerKey: req.GetLayerKey(),
		KernelDigest: req.GetKernelDigest(), BaseDigest: req.GetBaseDigest(), MemoryMB: int(req.GetMemoryMb()), EphemeralDiskMB: int(req.GetEphemeralDiskMb()),
	})
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if snap == nil {
		return nil, status.Error(codes.Internal, "execution template capture returned no snapshot")
	}
	return &vmmdpb.CaptureExecutionTemplateResponse{Snapshot: &vmmdpb.SnapshotRef{
		StorageKey: snap.StorageKey, VmstateStorageKey: snap.VMStateStorageKey, FcVersion: snap.FCVersion, MemBytes: snap.MemBytes, Networkless: true,
	}, VmstateBytes: info.VMStateBytes}, nil
}
