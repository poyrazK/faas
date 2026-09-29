package vmmdgrpc

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// maxResolvedEgressAddresses bounds one AllowResolvedEgress call; a DNS
// answer rarely carries more than a few dozen address records.
const maxResolvedEgressAddresses = 256

type resolvedEgressAllower interface {
	AllowResolvedEgress(ctx context.Context, source netip.Addr, addrs []netip.Addr, ttl time.Duration) error
}

// AllowResolvedEgress implements the ADR-373 DNS-gated egress hook: the
// node's bridge resolver reports a guest's answer and vmmd adds the
// addresses to that instance's egress_resolved set.
func (s *Server) AllowResolvedEgress(ctx context.Context, req *vmmdpb.AllowResolvedEgressRequest) (*vmmdpb.AllowResolvedEgressAck, error) {
	allower, ok := s.vmm.(resolvedEgressAllower)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "vmmd: AllowResolvedEgress not supported by this manager")
	}
	source, err := netip.ParseAddr(req.GetSourceIp())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "vmmd: AllowResolvedEgress: source_ip: %v", err)
	}
	if len(req.GetAddresses()) > maxResolvedEgressAddresses {
		return nil, status.Errorf(codes.InvalidArgument, "vmmd: AllowResolvedEgress: %d addresses exceeds %d", len(req.GetAddresses()), maxResolvedEgressAddresses)
	}
	addrs := make([]netip.Addr, 0, len(req.GetAddresses()))
	for _, raw := range req.GetAddresses() {
		a, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "vmmd: AllowResolvedEgress: address %q: %v", raw, err)
		}
		addrs = append(addrs, a)
	}
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	if err := allower.AllowResolvedEgress(ctx, source, addrs, ttl); err != nil {
		if errors.Is(err, fcvm.ErrResolvedEgressNoInstance) {
			return &vmmdpb.AllowResolvedEgressAck{Matched: false}, nil
		}
		return nil, status.Errorf(codes.Internal, "vmmd: AllowResolvedEgress: %v", err)
	}
	return &vmmdpb.AllowResolvedEgressAck{Matched: true}, nil
}
