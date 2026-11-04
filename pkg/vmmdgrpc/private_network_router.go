package vmmdgrpc

import (
	"context"
	"fmt"
	"net/netip"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) ReconcilePrivateNetworkPeerings(ctx context.Context, req *vmmdpb.ReconcilePrivateNetworkPeeringsRequest) (*vmmdpb.ReconcilePrivateNetworkPeeringsAck, error) {
	if req.GetAccountId() == "" || req.GetRegion() == "" || len(req.GetNetworks()) > 1000 || len(req.GetRoutes()) > 10000 {
		return nil, status.Error(codes.InvalidArgument, "invalid private router scope or size")
	}
	networks := make([]privatenetwork.FabricSpec, 0, len(req.GetNetworks()))
	for _, network := range req.GetNetworks() {
		spec, err := privatenetwork.NewFabricSpecFromValues(req.GetAccountId(), network.GetNetworkId(), req.GetRegion(), network.GetCidr())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		networks = append(networks, spec)
	}
	routes := make([]privatenetwork.PeeringRoute, 0, len(req.GetRoutes()))
	for _, route := range req.GetRoutes() {
		cidr, err := netip.ParsePrefix(route.GetDestinationCidr())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid peering CIDR: %v", err))
		}
		routes = append(routes, privatenetwork.PeeringRoute{FromNetworkID: route.GetFromNetworkId(), DestinationNetwork: route.GetDestinationNetworkId(), DestinationCIDR: cidr})
	}
	if _, err := privatenetwork.RouterPolicyCommands(req.GetAccountId(), req.GetRegion(), networks, routes); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	router, ok := s.vmm.(interface {
		ReconcilePrivateNetworkPeerings(context.Context, string, string, []privatenetwork.FabricSpec, []privatenetwork.PeeringRoute) error
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "private network routing unavailable")
	}
	if err := router.ReconcilePrivateNetworkPeerings(ctx, req.GetAccountId(), req.GetRegion(), networks, routes); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &vmmdpb.ReconcilePrivateNetworkPeeringsAck{}, nil
}
