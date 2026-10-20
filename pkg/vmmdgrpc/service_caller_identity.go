package vmmdgrpc

import (
	"context"
	"net/netip"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) RestoreManagedServiceProbe(ctx context.Context, req *vmmdpb.RestoreAppTaskRequest) (*vmmdpb.RestoreAppTaskResponse, error) {
	if _, ok := s.vmm.(interface {
		ResolveServiceCaller(netip.Addr) (fcvm.ServiceCallerIdentity, bool)
	}); !ok {
		return nil, status.Error(codes.Unimplemented, "managed service probes require live caller identity")
	}
	return s.RestoreAppTask(ctx, req)
}

func (s *Server) ResolveServiceCaller(_ context.Context, req *vmmdpb.ResolveServiceCallerRequest) (*vmmdpb.ResolveServiceCallerResponse, error) {
	address, err := netip.ParseAddr(req.GetSourceIp())
	if err != nil || !address.Is4() {
		return nil, status.Error(codes.InvalidArgument, "invalid service caller address")
	}
	provider, ok := s.vmm.(interface {
		ResolveServiceCaller(netip.Addr) (fcvm.ServiceCallerIdentity, bool)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "live service caller identity unavailable")
	}
	identity, matched := provider.ResolveServiceCaller(address)
	if !matched {
		return &vmmdpb.ResolveServiceCallerResponse{}, nil
	}
	return &vmmdpb.ResolveServiceCallerResponse{Matched: matched, InstanceId: identity.InstanceID,
		AccountId: identity.AccountID, AppId: identity.AppID, DeploymentId: identity.DeploymentID, AppTask: identity.AppTask, JobTask: identity.JobTask}, nil
}
