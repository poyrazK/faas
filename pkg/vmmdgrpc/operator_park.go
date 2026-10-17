package vmmdgrpc

// adr: 1525. The store authorizes one effect, independently of wire retries.

import (
	"context"
	"fmt"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type operatorParkVMMAPI interface {
	ExecuteOperatorPark(context.Context, state.OperatorParkTarget) (state.OperatorParkReceipt, error)
}

func (s *Server) WithOperatorParkStore(store state.OperatorParkStore, nodeID string) *Server {
	s.parkStore = store
	s.parkNodeID = nodeID
	return s
}

func (s *Server) ExecuteOperatorPark(ctx context.Context, req *vmmdpb.ExecuteOperatorParkRequest) (*vmmdpb.ExecuteOperatorParkResponse, error) {
	vmm, ok := s.vmm.(operatorParkVMMAPI)
	if !ok || s.parkStore == nil || s.parkNodeID == "" {
		return nil, status.Error(codes.Unimplemented, "durable operator park unavailable")
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	if req == nil || runtimeadmission.RejectUnknown(req) != nil {
		return nil, admissionStatus(runtimeadmission.ErrInvalid)
	}
	e, execute, err := s.parkStore.StartOperatorPark(ctx, req.GetIntentId(), req.GetClaimToken(), s.parkNodeID)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if !execute {
		return &vmmdpb.ExecuteOperatorParkResponse{Phase: e.Phase, Acknowledged: e.StoppedAt != nil}, nil
	}
	budget := api.OperatorParkRPCTimeout
	if grace := time.Duration(e.Target.GraceSeconds)*time.Second + api.OperatorParkCleanupTimeout; grace > budget {
		budget = grace
	}
	effectCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	started := time.Now()
	receipt, err := vmm.ExecuteOperatorPark(effectCtx, e.Target)
	s.ops.Observe("OperatorPark", time.Since(started), err)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("park outcome requires reconciliation: %v", err))
	}
	if err := receipt.Validate(e.Target); err != nil {
		return nil, admissionStatus(err)
	}
	ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorRestartPersistenceTimeout)
	defer ackCancel()
	for range api.OperatorRestartPersistenceAttempts {
		if err = s.parkStore.AcknowledgeOperatorPark(ackCtx, e.IntentID, e.EffectToken, receipt); err == nil {
			if s.streamBridges != nil {
				s.streamBridges.forget(ackCtx, e.Target.InstanceID)
			}
			return &vmmdpb.ExecuteOperatorParkResponse{Phase: state.ParkAcknowledged, Acknowledged: true}, nil
		}
		if ackCtx.Err() != nil {
			break
		}
	}
	return nil, status.Error(codes.Unavailable, "park finished but its durable receipt is uncertain")
}
