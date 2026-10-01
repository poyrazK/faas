package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
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
//
// The vmmd client cache is keyed by compute_nodes.id, while this daemon knows
// its node by manifest name (FAAS_NODE_NAME, for example fsn-2.faas). Passing
// the name looked a hostname up in a UUID column, so no answer was ever
// registered and DNS-gated egress dropped every guest connection: builds
// could not reach npm or nodejs.org on the first production-us fleet.
func newResolvedEgressHook(clients vmmdClientLookup, localNode *localNodeID) gateway.ResolvedEgressHook {
	return func(ctx context.Context, remote string, addrs []netip.Addr, ttl time.Duration) error {
		host := remote
		if h, _, err := net.SplitHostPort(remote); err == nil {
			host = h
		}
		nodeID, err := localNode.Get(ctx)
		if err != nil {
			return err
		}
		cli, closer, ok := clients.ClientFor(ctx, nodeID)
		if !ok {
			localNode.Forget()
			return fmt.Errorf("local vmmd %s (%s) unavailable", localNode.name, nodeID)
		}
		defer func() { _ = closer.Close() }()
		return allowResolvedEgress(ctx, cli, host, addrs, ttl)
	}
}

type computeNodeByName interface {
	ComputeNodeByName(ctx context.Context, name string) (state.ComputeNode, error)
}

// localNodeID resolves and caches this node's compute_nodes.id from its
// manifest name. A failed vmmd lookup forgets the cached id so a re-registered
// node is picked up on the next query.
type localNodeID struct {
	store computeNodeByName
	name  string

	mu sync.Mutex
	id string
}

func newLocalNodeID(store computeNodeByName, name string) *localNodeID {
	return &localNodeID{store: store, name: name}
}

func (l *localNodeID) Get(ctx context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.id != "" {
		return l.id, nil
	}
	node, err := l.store.ComputeNodeByName(ctx, l.name)
	if err != nil {
		return "", fmt.Errorf("resolve local compute node %s: %w", l.name, err)
	}
	l.id = node.ID
	return l.id, nil
}

func (l *localNodeID) Forget() {
	l.mu.Lock()
	l.id = ""
	l.mu.Unlock()
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
