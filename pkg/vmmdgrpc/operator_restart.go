package vmmdgrpc

import (
	"context"
	"fmt"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type operatorRestartVMMAPI interface {
	OperatorRestartGeneration(string) (string, error)
	DestroyOperatorRestart(context.Context, string, string) error
}

// WithOperatorRestartStore enables the restart protocol only with a durable
// store and the actual local compute identity, including default-local's UUID.
func (s *Server) WithOperatorRestartStore(store state.OperatorRestartStore, nodeID string) *Server {
	s.restartStore, s.restartNodeID = store, nodeID
	return s
}

func (s *Server) GetOperatorRestartGeneration(_ context.Context, req *vmmdpb.GetOperatorRestartGenerationRequest) (*vmmdpb.GetOperatorRestartGenerationResponse, error) {
	vmm, ok := s.vmm.(operatorRestartVMMAPI)
	if !ok || s.restartStore == nil || s.restartNodeID == "" {
		return nil, status.Error(codes.Unimplemented, "durable operator restart unavailable")
	}
	generation, err := vmm.OperatorRestartGeneration(req.GetInstance())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &vmmdpb.GetOperatorRestartGenerationResponse{Generation: generation}, nil
}

func (s *Server) ExecuteOperatorRestartDestroy(ctx context.Context, req *vmmdpb.ExecuteOperatorRestartDestroyRequest) (*vmmdpb.ExecuteOperatorRestartDestroyResponse, error) {
	vmm, ok := s.vmm.(operatorRestartVMMAPI)
	if !ok || s.restartStore == nil || s.restartNodeID == "" {
		return nil, status.Error(codes.Unimplemented, "durable operator restart unavailable")
	}
	e, execute, err := s.restartStore.StartOperatorRestartDestroy(ctx, req.GetIntentId(), req.GetClaimToken(), s.restartNodeID)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if !execute {
		return &vmmdpb.ExecuteOperatorRestartDestroyResponse{Phase: e.Phase, Acknowledged: e.DestroyedAt != nil}, nil
	}
	destroyCtx, cancel := context.WithTimeout(ctx, api.OperatorRestartRPCTimeout)
	defer cancel()
	started := time.Now()
	err = vmm.DestroyOperatorRestart(destroyCtx, e.Target.InstanceID, e.Target.Generation)
	s.ops.Observe("OperatorRestartDestroy", time.Since(started), err)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("restart teardown outcome requires reconciliation: %v", err))
	}
	// Persist only the acknowledgement on retry; never invoke teardown again.
	ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorRestartPersistenceTimeout)
	defer ackCancel()
	for range api.OperatorRestartPersistenceAttempts {
		if err = s.restartStore.AcknowledgeOperatorRestartDestroy(ackCtx, e.IntentID, e.DestroyToken); err == nil {
			return &vmmdpb.ExecuteOperatorRestartDestroyResponse{Phase: state.RestartDestroyed, Acknowledged: true}, nil
		}
		if ackCtx.Err() != nil {
			break
		}
	}
	return nil, status.Error(codes.Unavailable, "teardown finished but its durable acknowledgement is uncertain")
}
