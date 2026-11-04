package vmmdgrpc

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/migrationadmissionwire"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type migrationAdmissionVMM interface {
	MigrationAdmissionAvailable(string) bool
	AdoptMigrationAdmission(context.Context, state.MigrationAdmissionPlan, fcvm.WakeRequest) (*fcvm.Instance, error)
	RetireMigrationAdmission(context.Context, state.MigrationAdmissionPlan) (string, error)
}

func (s *Server) adoptMigrationAdmission(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	vmm, ok := s.vmm.(migrationAdmissionVMM)
	if !ok || s.migrationAdmissions == nil || s.nodeID == "" {
		return nil, status.Error(codes.Unimplemented, "durable migration adoption unavailable")
	}
	if req.GetAdmissionId() == "" {
		return nil, status.Error(codes.FailedPrecondition, "migration adoption requires a durable destination admission")
	}
	if !vmm.MigrationAdmissionAvailable(s.nodeID) {
		a, err := s.migrationAdmissions.MigrationAdmissionByID(ctx, req.GetAdmissionId())
		if err != nil || a.Phase == state.MigrationReserved {
			return nil, status.Error(codes.FailedPrecondition, "durable local native migration ownership unavailable")
		}
	}
	ctx = withIncomingCorrelation(ctx)
	fields, _ := wire.FromContext(ctx)
	fields.WakeID = req.GetWakeId()
	ctx = wire.WithContext(ctx, fields)
	digest, err := migrationadmissionwire.Digest(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid migration adoption request")
	}
	wake, err := toMigrationWakeRequest(ctx, req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	wake.NodeID = s.nodeID
	a, execute, err := s.migrationAdmissions.StartMigrationAdoption(ctx, req.GetAdmissionId(), s.nodeID, digest)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if !execute {
		if a.Phase != state.MigrationAdopted && a.Phase != state.MigrationCommitted {
			return nil, status.Error(codes.FailedPrecondition, state.ErrMigrationAdoptionUnresolved.Error())
		}
		out := new(vmmdpb.AdoptMigratedInstanceResponse)
		if err := proto.Unmarshal(a.AdoptionReceipt, out); err != nil {
			return nil, status.Error(codes.Internal, "retained migration adoption receipt is invalid")
		}
		return out, nil
	}
	if err := s.validateMigrationLease(ctx, req.GetInstanceId(), req.GetLeaseToken()); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	inst, err := vmm.AdoptMigrationAdmission(ctx, a.MigrationAdmissionPlan, wake)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "migration adoption requires reconciliation: "+err.Error())
	}
	if inst == nil {
		return nil, status.Error(codes.Internal, "migration adoption returned no instance")
	}
	out := migrationAdoptionResponse(inst, wake, req.GetWakeId())
	out.AdmissionId = a.ID
	receipt, err := (proto.MarshalOptions{Deterministic: true}).Marshal(out)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot encode migration adoption receipt")
	}
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	if err := s.migrationAdmissions.AcknowledgeMigrationAdoption(ackCtx, a.ID, s.nodeID, digest, receipt); err != nil {
		return nil, status.Error(codes.Unavailable, "migration adoption acknowledgement is uncertain")
	}
	return out, nil
}

func migrationAdoptionResponse(inst *fcvm.Instance, wake fcvm.WakeRequest, wakeID string) *vmmdpb.AdoptMigratedInstanceResponse {
	method := vmmdpb.WakeMethod_WAKE_UNKNOWN
	switch inst.Method {
	case fcvm.WakeRestore:
		method = vmmdpb.WakeMethod_WAKE_RESTORE
	case fcvm.WakeColdBoot:
		method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
	}
	return &vmmdpb.AdoptMigratedInstanceResponse{SupportsSecretAliases: true, ImageHealthcheckVerified: wake.ImageHealthcheckRequired && inst.ImageHealthcheckRequired && !inst.Paused, SupportsImageHealthcheckMonitoring: true, HostIp: addrOrEmpty(inst.Lease.HostIP), Netns: inst.Net.Netns, GuestUid: int32(inst.Lease.UID), Method: method, WakeId: wakeID}
}

func (s *Server) RetireMigrationAdoption(ctx context.Context, req *vmmdpb.RetireMigrationAdoptionRequest) (*vmmdpb.RetireMigrationAdoptionResponse, error) {
	start := time.Now()
	out, err := s.retireMigrationAdoption(ctx, req)
	s.ops.Observe("RetireMigrationAdoption", time.Since(start), err)
	return out, err
}

func (s *Server) retireMigrationAdoption(ctx context.Context, req *vmmdpb.RetireMigrationAdoptionRequest) (*vmmdpb.RetireMigrationAdoptionResponse, error) {
	vmm, ok := s.vmm.(migrationAdmissionVMM)
	if !ok || s.migrationAdmissions == nil || s.nodeID == "" {
		return nil, status.Error(codes.Unimplemented, "durable migration retirement unavailable")
	}
	a, err := s.migrationAdmissions.RequestMigrationAdmissionCleanup(ctx, req.GetAdmissionId(), s.nodeID)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if a.Phase == state.MigrationCleaned || a.Phase == state.MigrationCommitted {
		return &vmmdpb.RetireMigrationAdoptionResponse{Phase: a.Phase, Cleaned: a.Phase == state.MigrationCleaned}, nil
	}
	proof, err := vmm.RetireMigrationAdmission(ctx, a.MigrationAdmissionPlan)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "migration destination cleanup remains unresolved: "+err.Error())
	}
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	if err := s.migrationAdmissions.AcknowledgeMigrationAdmissionCleanup(ackCtx, a.ID, s.nodeID, proof); err != nil {
		return nil, status.Error(codes.Unavailable, "migration cleanup acknowledgement is uncertain")
	}
	return &vmmdpb.RetireMigrationAdoptionResponse{Phase: state.MigrationCleaned, Cleaned: true}, nil
}

// The cursor advances past failures so one unresolved owner cannot starve
// another destination's cleanup. Failed attempts retain all durable capacity.
func (s *Server) reconcileMigrationAdmissions(ctx context.Context, now time.Time) {
	if s.migrationAdmissions == nil || s.nodeID == "" {
		return
	}
	rows, err := s.migrationAdmissions.ListMigrationAdmissionsForRecovery(ctx, s.nodeID, now, s.migrationAdmissionAfter, api.MigrationAdmissionRecoveryBatchMax)
	if err != nil {
		if s.log != nil {
			s.log.Warn("vmmd: incoming migration recovery read failed", "err", err)
		}
		return
	}
	if len(rows) == 0 {
		s.migrationAdmissionAfter = ""
		return
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	for _, a := range rows {
		if recoveryCtx.Err() != nil {
			break
		}
		s.migrationAdmissionAfter = a.ID
		_, err := s.RetireMigrationAdoption(recoveryCtx, &vmmdpb.RetireMigrationAdoptionRequest{AdmissionId: a.ID})
		if err != nil && !errors.Is(err, context.Canceled) && s.log != nil {
			s.log.Warn("vmmd: incoming migration cleanup retained", "admission", a.ID, "instance", a.InstanceID, "err", err)
		}
	}
}
