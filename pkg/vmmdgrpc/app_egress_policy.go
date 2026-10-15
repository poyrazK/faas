package vmmdgrpc

import (
	"context"
	"net/netip"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
)

func (s *Server) UpdateAppEgressPolicy(ctx context.Context, req *vmmdpb.UpdateAppEgressPolicyRequest) (_ *vmmdpb.UpdateAppEgressPolicyAck, err error) {
	start := time.Now()
	defer func() { s.ops.Observe("UpdateAppEgressPolicy", time.Since(start), err) }()
	if req.GetAppId() == "" || req.GetRevision() <= 0 {
		return nil, grpcerr.ToStatus(toProblem(api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid egress policy", "app_id and a positive revision are required")))
	}
	allowlist, err := toEgressAllowlist(req.GetEgressAllowlist())
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	for _, prefix := range allowlist {
		if prefix.Bits() == 0 {
			return nil, grpcerr.ToStatus(toProblem(api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
				"Invalid egress policy CIDR", "default routes are forbidden")))
		}
	}
	for _, port := range req.GetEgressPorts() {
		if _, forbidden := api.TenantEgressForbiddenPort(int(port)); port == 0 || port > 65535 || forbidden {
			return nil, grpcerr.ToStatus(toProblem(api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
				"Invalid egress policy port", "ports must satisfy platform restrictions")))
		}
	}
	updater, ok := s.vmm.(interface {
		UpdateAppEgressPolicy(context.Context, string, int64, []netip.Prefix, []uint16) error
	})
	if !ok {
		return nil, grpcerr.ToStatus(toProblem(api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Revisioned egress unavailable", "this vmmd cannot order complete egress policy updates")))
	}
	if err := updater.UpdateAppEgressPolicy(ctx, req.GetAppId(), req.GetRevision(), allowlist, egressPortsFromWire(req.GetEgressPorts())); err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.UpdateAppEgressPolicyAck{Revision: req.GetRevision()}, nil
}
