package vmmdgrpc

import (
	"context"
	"errors"
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

type environmentQualificationArtifactRetirementAPI interface {
	RetireEnvironmentQualificationArtifacts(context.Context, state.EnvironmentQualificationExecution,
		state.EnvironmentQualificationExecution, state.EnvironmentQualificationSmokeReceipt, string) error
}

var _ environmentQualificationSnapshotAPI = (*fcvm.Manager)(nil)
var _ environmentQualificationArtifactRetirementAPI = (*fcvm.Manager)(nil)

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

func (s *Server) RetireEnvironmentQualificationArtifacts(ctx context.Context,
	req *vmmdpb.RetireEnvironmentQualificationArtifactsRequest) (response *vmmdpb.RetireEnvironmentQualificationArtifactsResponse, result error) {
	start := time.Now()
	defer func() { s.ops.Observe("RetireEnvironmentQualificationArtifacts", time.Since(start), result) }()
	retirer, ok := s.vmm.(environmentQualificationArtifactRetirementAPI)
	if !ok {
		return nil, qualificationUnavailable()
	}
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 || req.GetSmokeReceipt() == nil ||
		len(req.GetSmokeReceipt().ProtoReflect().GetUnknown()) != 0 || req.GetCaptureId() == "" {
		return nil, qualificationStatus(state.ErrInvalidArgument)
	}
	ctx = withIncomingCorrelation(ctx)
	capture, err := qualificationwire.RetirementExecutionFromProto(req.GetCaptureExecution())
	if err != nil {
		return nil, qualificationStatus(err)
	}
	capture, err = s.validateQualificationEnvelope(ctx, capture)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	restored, err := qualificationwire.RetirementExecutionFromProto(req.GetRestoreExecution())
	if err != nil || restored.NodeID != s.nodeID {
		return nil, qualificationStatus(errors.Join(state.ErrInvalidArgument, err))
	}
	proof := req.GetSmokeReceipt()
	smoke := state.EnvironmentQualificationSmokeReceipt{RequestID: proof.GetRequestId(), Attempt: proof.GetAttempt(), GraphID: proof.GetGraphId(),
		CaptureInstanceID: proof.GetCaptureInstanceId(), InstanceID: proof.GetInstanceId(), Resource: proof.GetResource(), PolicyID: proof.GetPolicyId(),
		PolicySHA256: proof.GetPolicySha256(), ResultSHA256: proof.GetResultSha256(), RecordedAt: time.UnixMilli(proof.GetRecordedAtUnixMillis()).UTC()}
	if err := retirer.RetireEnvironmentQualificationArtifacts(ctx, capture, restored, smoke, req.GetCaptureId()); err != nil {
		return nil, qualificationStatus(err)
	}
	return &vmmdpb.RetireEnvironmentQualificationArtifactsResponse{CaptureId: req.GetCaptureId(), Retired: true}, nil
}
