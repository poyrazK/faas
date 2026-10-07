package vmmdgrpc

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/qualificationwire"
	"github.com/onebox-faas/faas/pkg/state"
)

// Additive: existing attempt-aware creation/retirement does not imply capture
// support, and a generic WarmSnapshot is never a fallback for this operation.
type environmentQualificationSnapshotAPI interface {
	CaptureEnvironmentQualification(context.Context, state.EnvironmentQualificationExecution) (state.EnvironmentQualificationSnapshot, error)
}

var _ environmentQualificationSnapshotAPI = (*fcvm.Manager)(nil)

func (s *Server) CaptureEnvironmentQualification(ctx context.Context, req *vmmdpb.CaptureEnvironmentQualificationRequest) (response *vmmdpb.CaptureEnvironmentQualificationResponse, result error) {
	start := time.Now()
	defer func() { s.ops.Observe("CaptureEnvironmentQualification", time.Since(start), result) }()
	vmm, ok := s.vmm.(environmentQualificationSnapshotAPI)
	if !ok {
		return nil, qualificationUnavailable()
	}
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 {
		return nil, qualificationStatus(state.ErrInvalidArgument)
	}
	ctx = withIncomingCorrelation(ctx)
	frame, err := s.qualificationFrame(ctx, req.GetExecution())
	if err != nil {
		return nil, qualificationStatus(err)
	}
	proof, err := vmm.CaptureEnvironmentQualification(ctx, frame)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	encoded, err := qualificationwire.SnapshotToProto(frame, proof)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	encodedFrame, err := qualificationwire.ExecutionToProto(frame)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	return &vmmdpb.CaptureEnvironmentQualificationResponse{Execution: encodedFrame, Snapshot: encoded}, nil
}
