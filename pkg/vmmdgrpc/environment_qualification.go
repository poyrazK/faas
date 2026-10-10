package vmmdgrpc

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/qualificationwire"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Optional and attempt-aware: neither generic Wake nor Destroy implements it.
// Manager requires native journal ownership and an explicit local compute ID.
type environmentQualificationVMMAPI interface {
	WakeEnvironmentQualification(context.Context, state.EnvironmentQualificationExecution, fcvm.WakeRequest) (*fcvm.Instance, error)
	RetireEnvironmentQualification(context.Context, state.EnvironmentQualificationExecution) (state.EnvironmentQualificationRetirement, error)
}

// Restore is a separate optional capability. Generic Wake may cold-boot after
// a snapshot miss; qualification restore must consume the exact capture and
// return only after the private native restore path confirms success.
type environmentQualificationRestoreVMMAPI interface {
	RestoreEnvironmentQualification(context.Context, state.EnvironmentQualificationExecution, fcvm.WakeRequest) (*fcvm.Instance, error)
}

type environmentQualificationJobVMMAPI interface {
	BootEnvironmentQualificationJob(context.Context, state.EnvironmentQualificationExecution, fcvm.JobBootRequest) (*fcvm.Instance, error)
}

var _ environmentQualificationVMMAPI = (*fcvm.Manager)(nil)

func (s *Server) qualificationFrame(ctx context.Context, p *vmmdpb.EnvironmentQualificationExecution) (state.EnvironmentQualificationExecution, error) {
	frame, err := qualificationwire.ExecutionFromProto(p)
	if err != nil {
		return frame, err
	}
	return s.validateQualificationEnvelope(ctx, frame)
}

func (s *Server) qualificationRestoreFrame(ctx context.Context, p *vmmdpb.EnvironmentQualificationExecution) (state.EnvironmentQualificationExecution, error) {
	frame, err := qualificationwire.RestoreExecutionFromProto(p)
	if err != nil {
		return frame, err
	}
	return s.validateQualificationEnvelope(ctx, frame)
}

func (s *Server) qualificationRetirementFrame(ctx context.Context, p *vmmdpb.EnvironmentQualificationExecution) (state.EnvironmentQualificationExecution, error) {
	frame, err := qualificationwire.RetirementExecutionFromProto(p)
	if err != nil {
		return frame, err
	}
	return s.validateQualificationEnvelope(ctx, frame)
}

func (s *Server) validateQualificationEnvelope(ctx context.Context, frame state.EnvironmentQualificationExecution) (state.EnvironmentQualificationExecution, error) {
	if s.nodeID == "" || frame.NodeID != s.nodeID {
		return frame, state.ErrConflict
	}
	md, _ := metadata.FromIncomingContext(ctx)
	for key, want := range map[string]string{"x-faas-node-id": frame.NodeID, "x-faas-instance-id": frame.InstanceID,
		"x-faas-app-id": frame.AppID, "x-faas-deployment-id": frame.DeploymentID, "x-faas-wake-id": frame.WakeID} {
		values := md.Get(key)
		if len(values) != 1 || values[0] != want {
			return frame, state.ErrInvalidArgument
		}
	}
	return frame, nil
}

func qualificationWakeRequest(ctx context.Context, frame state.EnvironmentQualificationExecution, req *vmmdpb.CreateEnvironmentQualificationRequest) (fcvm.WakeRequest, error) {
	wake, err := toColdBootRequest(ctx, &vmmdpb.CreateColdBootRequest{Instance: frame.InstanceID, App: req.GetApp(), Plan: req.GetPlan(), AccountId: req.GetAccountId()})
	account, accountErr := uuid.Parse(wake.AccountID)
	if err != nil || accountErr != nil || account == uuid.Nil || !wake.Plan.Valid() || wake.AppID != frame.AppID || wake.DeploymentID != frame.DeploymentID ||
		wake.MemSizeMiB != frame.RAMMB || wake.VcpuCount < 1 || wake.BaseKey == "" || frame.Artifact.RootfsKey == "" || wake.LayerKey != frame.Artifact.RootfsKey {
		return fcvm.WakeRequest{}, state.ErrInvalidArgument
	}
	return wake, nil
}

// Creation acknowledges a private runtime only. It never invokes the generic
// fallback, bridge prewarm, release command or graph activation paths.
func (s *Server) CreateEnvironmentQualification(ctx context.Context, req *vmmdpb.CreateEnvironmentQualificationRequest) (response *vmmdpb.CreateEnvironmentQualificationResponse, result error) {
	start := time.Now()
	defer func() { s.ops.Observe("CreateEnvironmentQualification", time.Since(start), result) }()
	vmm, ok := s.vmm.(environmentQualificationVMMAPI)
	if !ok {
		return nil, qualificationUnavailable()
	}
	ctx = withIncomingCorrelation(ctx)
	frame, err := s.qualificationFrame(ctx, req.GetExecution())
	if err != nil {
		return nil, qualificationStatus(err)
	}
	wake, err := qualificationWakeRequest(ctx, frame, req)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	inst, err := vmm.WakeEnvironmentQualification(ctx, frame, wake)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	if inst == nil || inst.Lease.Instance != frame.InstanceID || inst.Method != fcvm.WakeColdBoot {
		return nil, qualificationStatus(state.ErrConflict)
	}
	encoded, err := qualificationwire.ExecutionToProto(frame)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	return &vmmdpb.CreateEnvironmentQualificationResponse{Execution: encoded,
		Wake: wakeResponseFromInstance(frame.InstanceID, wake, inst, vmmdpb.WakeMethod_WAKE_COLD_BOOT)}, nil
}

func (s *Server) RestoreEnvironmentQualification(ctx context.Context, req *vmmdpb.RestoreEnvironmentQualificationRequest) (response *vmmdpb.RestoreEnvironmentQualificationResponse, result error) {
	start := time.Now()
	defer func() { s.ops.Observe("RestoreEnvironmentQualification", time.Since(start), result) }()
	vmm, ok := s.vmm.(environmentQualificationRestoreVMMAPI)
	if !ok {
		return nil, qualificationUnavailable()
	}
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 {
		return nil, qualificationStatus(state.ErrInvalidArgument)
	}
	ctx = withIncomingCorrelation(ctx)
	frame, err := s.qualificationRestoreFrame(ctx, req.GetExecution())
	if err != nil {
		return nil, qualificationStatus(err)
	}
	wake, err := qualificationWakeRequest(ctx, frame, &vmmdpb.CreateEnvironmentQualificationRequest{
		App: req.GetApp(), Plan: req.GetPlan(), AccountId: req.GetAccountId(),
	})
	if err != nil {
		return nil, qualificationStatus(err)
	}
	inst, err := vmm.RestoreEnvironmentQualification(ctx, frame, wake)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	if inst == nil || inst.Lease.Instance != frame.InstanceID || inst.Method != fcvm.WakeRestore || inst.Paused {
		return nil, qualificationStatus(state.ErrConflict)
	}
	encoded, err := qualificationwire.RestoreExecutionToProto(frame)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	return &vmmdpb.RestoreEnvironmentQualificationResponse{Execution: encoded,
		Wake: wakeResponseFromInstance(frame.InstanceID, wake, inst, vmmdpb.WakeMethod_WAKE_RESTORE)}, nil
}

// Cleanup accepts the original frame or its distinct restore target after
// deadline/supersession. Only the matching attempt-aware host journal can
// acknowledge retirement; absence cannot.
func (s *Server) RetireEnvironmentQualification(ctx context.Context, req *vmmdpb.RetireEnvironmentQualificationRequest) (response *vmmdpb.RetireEnvironmentQualificationResponse, result error) {
	start := time.Now()
	defer func() { s.ops.Observe("RetireEnvironmentQualification", time.Since(start), result) }()
	vmm, ok := s.vmm.(environmentQualificationVMMAPI)
	if !ok {
		return nil, qualificationUnavailable()
	}
	frame, err := s.qualificationRetirementFrame(ctx, req.GetExecution())
	if err != nil {
		return nil, qualificationStatus(err)
	}
	proof, err := vmm.RetireEnvironmentQualification(ctx, frame)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	encodedProof, err := qualificationwire.NativeRetirementToProto(proof)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	encodedFrame, err := qualificationwire.RetirementExecutionToProto(frame)
	if err != nil {
		return nil, qualificationStatus(err)
	}
	return &vmmdpb.RetireEnvironmentQualificationResponse{Execution: encodedFrame, Retirement: encodedProof}, nil
}

// Do not send journal errors or cleanup capabilities through diagnostic text.
func qualificationStatus(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "qualification deadline elapsed")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "qualification was canceled")
	case errors.Is(err, state.ErrInvalidArgument):
		return grpcerr.ToStatus(api.NewProblem(400, api.CodeValidation, "Invalid qualification execution", "the original execution contract is invalid"))
	case errors.Is(err, state.ErrConflict):
		return grpcerr.ToStatus(api.NewProblem(409, api.CodeEnvironmentQualificationUnconfirmed, "Qualification unconfirmed", "original ownership or complete native retirement is unconfirmed"))
	default:
		return grpcerr.ToStatus(api.NewProblem(500, api.CodeInternal, "Qualification failed", "the native operation could not be confirmed"))
	}
}

func qualificationUnavailable() error {
	return grpcerr.ToStatus(api.NewProblem(501, api.CodeNotImplemented, "Qualification unavailable", "attempt-aware native qualification is unavailable"))
}
