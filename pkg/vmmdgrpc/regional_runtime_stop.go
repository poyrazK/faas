package vmmdgrpc

// adr: 631
import (
	"context"
	"encoding/json"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type regionalRuntimeRetirer interface {
	RetireRegionalRuntime(context.Context, string, runtimequota.CapacityPermit, string) (runtimequota.CapacityStop, error)
}

// This RPC never falls back to Destroy, signs an assertion or reads a mutable
// instance row. The native owner joins late work and verifies resource absence.
func (s *Server) RetireRegionalRuntime(ctx context.Context, req *vmmdpb.RetireRegionalRuntimeRequest) (response *vmmdpb.RetireRegionalRuntimeResponse, result error) {
	start := time.Now()
	defer func() { s.ops.Observe("RetireRegionalRuntime", time.Since(start), result) }()
	cn, err := wire.PeerCN(ctx)
	if err != nil || cn != "schedd.faas" {
		return nil, status.Error(codes.Unauthenticated, "regional retirement requires authenticated schedd")
	}
	p, err := runtimequota.DecodeCapacityPermit(req.GetPermit())
	if err != nil || !runtimequota.MatchesID(p.InstanceID, req.GetInstance()) ||
		s.nodeID == "" || p.NodeID != s.nodeID ||
		(runtimequota.CapacityStop{Permit: p, FenceID: req.GetFenceId(), AdmissionFrozen: true}).Validate() != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid original regional runtime ownership")
	}
	retirer, ok := s.vmm.(regionalRuntimeRetirer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "native regional retirement unavailable")
	}
	stop, err := retirer.RetireRegionalRuntime(ctx, req.GetInstance(), p, req.GetFenceId())
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if stop.Validate() != nil || stop.Permit != p {
		return nil, status.Error(codes.FailedPrecondition, "native retirement ownership acknowledgement conflicts")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	raw, err := json.Marshal(stop)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode native regional retirement")
	}
	return &vmmdpb.RetireRegionalRuntimeResponse{Instance: req.GetInstance(), Stop: raw}, nil
}
