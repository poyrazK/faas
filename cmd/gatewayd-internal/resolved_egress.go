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
//
// When the name is one of the calling app's bound managed databases
// (ADR-20261026150000), the call also carries the database port so vmmd
// opens it to those addresses on any plan. Only names in the database index
// pay for resolving the caller.
func newResolvedEgressHook(clients vmmdClientLookup, localNode *localNodeID, databases *databaseEgressIndex, resolveCaller gateway.ServiceProxyCallerResolver) gateway.ResolvedEgressHook {
	return func(ctx context.Context, remote, name string, addrs []netip.Addr, ttl time.Duration) error {
		host := remote
		if h, _, err := net.SplitHostPort(remote); err == nil {
			host = h
		}
		var databasePort uint16
		if apps := databases.lookup(ctx, name); len(apps) > 0 && resolveCaller != nil {
			if appID, err := resolveCaller(ctx, remote); err == nil {
				databasePort = apps[appID]
			}
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
		return allowResolvedEgress(ctx, cli, host, addrs, ttl, databasePort)
	}
}

type databaseEgressSource interface {
	ManagedPostgresEgressEndpoints(ctx context.Context) ([]state.ManagedPostgresEgressEndpoint, error)
}

// databaseEgressIndexRefresh bounds how long a newly bound database stays
// closed: a guest that resolves it sooner fails its first connection and
// opens it on the next lookup, as libpq and Node resolve on every connect.
const databaseEgressIndexRefresh = 10 * time.Second

// databaseEgressIndex maps each bound managed database host to the apps bound
// to it and its port (ADR-20261026150000). It lives in memory because the
// resolver consults it for every forwarded answer; a stale index refreshes on
// the next lookup, and a failed refresh keeps the previous one.
type databaseEgressIndex struct {
	source databaseEgressSource
	now    func() time.Time

	mu       sync.Mutex
	byHost   map[string]map[string]uint16
	loadedAt time.Time
}

func newDatabaseEgressIndex(source databaseEgressSource) *databaseEgressIndex {
	return &databaseEgressIndex{source: source, now: time.Now}
}

// lookup returns the apps bound to name with each one's database port.
func (x *databaseEgressIndex) lookup(ctx context.Context, name string) map[string]uint16 {
	if x == nil || x.source == nil || name == "" {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.now().Sub(x.loadedAt) >= databaseEgressIndexRefresh {
		// A slow or failed read keeps the previous index until the next
		// interval rather than stalling every answer behind the lock.
		refreshCtx, cancel := context.WithTimeout(ctx, time.Second)
		endpoints, err := x.source.ManagedPostgresEgressEndpoints(refreshCtx)
		cancel()
		if err == nil {
			byHost := make(map[string]map[string]uint16, len(endpoints))
			for _, e := range endpoints {
				if byHost[e.Host] == nil {
					byHost[e.Host] = map[string]uint16{}
				}
				byHost[e.Host][e.AppID] = uint16(e.Port)
			}
			x.byHost = byHost
		}
		x.loadedAt = x.now()
	}
	return x.byHost[name]
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

func allowResolvedEgress(ctx context.Context, cli allowResolvedEgressClient, host string, addrs []netip.Addr, ttl time.Duration, databasePort uint16) error {
	req := &vmmdpb.AllowResolvedEgressRequest{SourceIp: host, TtlSeconds: uint32(min(ttl/time.Second, 1<<31)), DatabasePort: uint32(databasePort)}
	for _, a := range addrs {
		req.Addresses = append(req.Addresses, a.String())
	}
	if _, err := cli.AllowResolvedEgress(ctx, req); err != nil && status.Code(err) != codes.Unimplemented {
		return err
	}
	return nil
}
