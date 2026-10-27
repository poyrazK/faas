package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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
//
// Only the lookup that wins refreshMu reads Postgres; every concurrent answer
// reads the current index. The refresh used to run under the index lock, so
// every guest DNS answer on the node waited on a control-plane query.
type databaseEgressIndex struct {
	source databaseEgressSource
	now    func() time.Time

	refreshMu sync.Mutex
	mu        sync.RWMutex
	byHost    map[string]map[string]uint16
	loadedAt  time.Time
}

// databaseEgressEndpointsCap mirrors the LIMIT of
// ListManagedPostgresEgressEndpoints: a full page may have dropped bindings.
const databaseEgressEndpointsCap = 100000

func newDatabaseEgressIndex(source databaseEgressSource) *databaseEgressIndex {
	return &databaseEgressIndex{source: source, now: time.Now}
}

// lookup returns the apps bound to name with each one's database port.
func (x *databaseEgressIndex) lookup(ctx context.Context, name string) map[string]uint16 {
	if x == nil || x.source == nil || name == "" {
		return nil
	}
	x.mu.RLock()
	byHost, stale := x.byHost, x.now().Sub(x.loadedAt) >= databaseEgressIndexRefresh
	x.mu.RUnlock()
	if stale && x.refreshMu.TryLock() {
		byHost = x.refresh(ctx)
		x.refreshMu.Unlock()
	}
	return byHost[name]
}

// refresh reloads the index unless another lookup just did. A slow or failed
// read keeps the previous index until the next interval.
func (x *databaseEgressIndex) refresh(ctx context.Context) map[string]map[string]uint16 {
	x.mu.RLock()
	current, stale := x.byHost, x.now().Sub(x.loadedAt) >= databaseEgressIndexRefresh
	x.mu.RUnlock()
	if !stale {
		return current
	}
	refreshCtx, cancel := context.WithTimeout(ctx, time.Second)
	endpoints, err := x.source.ManagedPostgresEgressEndpoints(refreshCtx)
	cancel()
	if len(endpoints) >= databaseEgressEndpointsCap {
		slog.Error("gatewayd-internal: managed database egress index hit its row cap; bindings past it stay closed", "rows", len(endpoints))
	}
	x.mu.Lock()
	defer x.mu.Unlock()
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
	return x.byHost
}

type computeNodeByName interface {
	ComputeNodeByName(ctx context.Context, name string) (state.ComputeNode, error)
}

// localNodeID resolves and caches this node's compute_nodes.id from its
// manifest name. A failed vmmd lookup marks the cached id for a background
// re-resolve, so a re-registered node is picked up without every DNS answer
// querying the control plane meanwhile (a vmmd restart used to turn each
// answer into a CP query).
type localNodeID struct {
	store computeNodeByName
	name  string

	mu          sync.Mutex
	id          string
	recheck     bool
	rechecking  bool
	recheckDone chan struct{} // closed when a background re-resolve ends; tests wait on it
}

func newLocalNodeID(store computeNodeByName, name string) *localNodeID {
	return &localNodeID{store: store, name: name}
}

func (l *localNodeID) Get(ctx context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.id != "" {
		if l.recheck && !l.rechecking {
			l.rechecking = true
			l.recheckDone = make(chan struct{})
			go l.reresolve(l.recheckDone)
		}
		return l.id, nil
	}
	node, err := l.store.ComputeNodeByName(ctx, l.name)
	if err != nil {
		return "", fmt.Errorf("resolve local compute node %s: %w", l.name, err)
	}
	l.id = node.ID
	return l.id, nil
}

func (l *localNodeID) reresolve(done chan struct{}) {
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	node, err := l.store.ComputeNodeByName(ctx, l.name)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rechecking = false
	if err == nil && node.ID != "" {
		l.id = node.ID
		l.recheck = false
	}
}

// Forget marks the cached id for a background re-resolve; the cached id is
// served until a new one is read.
func (l *localNodeID) Forget() {
	l.mu.Lock()
	l.recheck = true
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
