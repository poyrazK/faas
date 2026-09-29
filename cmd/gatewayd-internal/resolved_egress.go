package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type vmmdClientLookup interface {
	ClientFor(ctx context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool)
}

type allowResolvedEgressClient interface {
	AllowResolvedEgress(ctx context.Context, in *vmmdpb.AllowResolvedEgressRequest, opts ...grpc.CallOption) (*vmmdpb.AllowResolvedEgressAck, error)
}

// newResolvedEgressHook reports each guest DNS answer to this node's vmmd
// (ADR-373), which lets the guest open TCP to the answered addresses. The
// query source is the instance's host-side address; vmmd maps it to the
// instance. A vmmd that predates the RPC has no DNS gate either, so
// Unimplemented is not an error during a rolling release.
func newResolvedEgressHook(clients vmmdClientLookup, nodeID string) gateway.ResolvedEgressHook {
	return func(ctx context.Context, remote string, addrs []netip.Addr, ttl time.Duration) error {
		host := remote
		if h, _, err := net.SplitHostPort(remote); err == nil {
			host = h
		}
		cli, closer, ok := clients.ClientFor(ctx, nodeID)
		if !ok {
			return fmt.Errorf("local vmmd %s unavailable", nodeID)
		}
		defer func() { _ = closer.Close() }()
		return allowResolvedEgress(ctx, cli, host, addrs, ttl)
	}
}

func allowResolvedEgress(ctx context.Context, cli allowResolvedEgressClient, host string, addrs []netip.Addr, ttl time.Duration) error {
	req := &vmmdpb.AllowResolvedEgressRequest{SourceIp: host, TtlSeconds: uint32(min(ttl/time.Second, 1<<31))}
	for _, a := range addrs {
		req.Addresses = append(req.Addresses, a.String())
	}
	if _, err := cli.AllowResolvedEgress(ctx, req); err != nil && status.Code(err) != codes.Unimplemented {
		return err
	}
	return nil
}
