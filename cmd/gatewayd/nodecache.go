// Issue #98 / ADR-028: gatewayd's per-node vmmd client cache + the
// pg_notify subscriber that evicts entries when a row mutates.
//
// Production wiring (one NodeClientCache per gatewayd process):
//
//	cache := gatewayd.NewNodeClientCache(pgStore, vmmdTLS, log)
//	defer cache.Close()
//	go cache.WatchEvictions(ctx, pool)  // LISTEN compute_node_changed
//	handler.WithForwarding(gateway.ForwardingReverseProxy(cache, log))
//
// The cache is the gateway's hot path: every Wake→proxy roundtrip
// lands on it. It's safe for concurrent use; pkg/gateway/forwardproxy.go
// owns the per-call refcount so an in-flight ForwardHTTP finishes
// against the conn it dialed before Evict closes the underlying
// transport. dialer errors fall through to 503 — the handler surfaces
// "node unavailable" and the client retries on the next hop.
//
// The resolver hook (gateway.SetNodeResolver) is set inside NewNodeClientCache
// once we know the pgStore; tests that don't care about the overlay
// path leave the package-level resolver as the zero value
// (returns "", false → ClientFor returns ok=false → 503).

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
)

// nodeCache bundles the gateway's per-node vmmd client cache with the
// subscribe-and-evict goroutine. Splitting it from cmd/gatewayd/main.go
// keeps main.go focused on listener wiring; tests can construct the
// cache without booting a full daemon.
type nodeCache struct {
	cache *gateway.NodeClientCache
	log   *slog.Logger
}

// newNodeCache wires the production cache: a *gateway.NodeClientCache
// whose dialer goes through pkg/wire.DialContext, with a resolver that
// asks pgStore for compute_nodes rows by id. The package-level
// gateway.SetNodeResolver is installed once at construction time so
// pkg/gateway doesn't have to import pkg/state (CLAUDE.md ownership).
//
// vmmdTLS may be nil for unix-only targets (single-box dev). tcp/dns
// targets must come with mTLS; the wire helper refuses a nil TLS on a
// non-unix scheme (issue #95).
func newNodeCache(store *state.PgStore, vmmdTLS *tls.Config, log *slog.Logger) *nodeCache {
	if log == nil {
		log = slog.Default()
	}
	gateway.SetNodeResolver(func(ctx context.Context, nodeID string) (string, bool) {
		n, err := store.ComputeNodeByID(ctx, nodeID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return "", false
			}
			log.Warn("gateway: resolve compute_node for dial", "node", nodeID, "err", err.Error())
			return "", false
		}
		if !n.Active {
			return "", false
		}
		return n.TargetURL, true
	})
	cache := gateway.NewNodeClientCache(
		func(ctx context.Context, target string) (*grpc.ClientConn, error) {
			// Issue #120: route through pkg/overlay so the cross-box
			// dial primitive lives in one place. The cache above is
			// unrelated; only the underlying wire dial is swapped.
			return overlay.Dial(ctx, overlay.New(target), vmmdTLS)
		},
		log,
	)
	return &nodeCache{cache: cache, log: log}
}

// Forwarding returns the per-node http.Handler factory. cmd/gatewayd
// installs it on the gateway.Handler via WithForwarding so every
// request dispatches through the cache.
func (n *nodeCache) Forwarding() func(nodeID string) http.Handler {
	return gateway.ForwardingReverseProxy(n.cache, n.log)
}

// Close shuts down every cached *grpc.ClientConn. Called once at
// shutdown; subsequent ClientFor calls return ok=false so any
// in-flight listener draining sees "node unavailable" → 503.
func (n *nodeCache) Close() error { return n.cache.Close() }

// WatchEvictions subscribes to the compute_node_changed pg_notify
// channel and calls cache.Evict(nodeID) for each notification. Runs
// until ctx is cancelled. Uses db.SubscribeWithReconnect so a Postgres
// restart doesn't strand the cache in a stale state (the parallel to
// watchInvalidations on the routing side).
func (n *nodeCache) WatchEvictions(ctx context.Context, pool *pgxpool.Pool) {
	notif, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyComputeNodeChanged}, n.log)
	if err != nil {
		n.log.Error("gatewayd: subscribe compute_node_changed", "err", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case got, ok := <-notif:
			if !ok {
				return
			}
			var p struct {
				NodeID string `json:"node_id"`
				Active bool   `json:"active"`
			}
			if err := json.Unmarshal([]byte(got.Payload), &p); err != nil || p.NodeID == "" {
				n.log.Warn("gatewayd: bad compute_node_changed payload", "payload", got.Payload)
				continue
			}
			// Evict on every mutation: the resolver re-reads the row
			// (active / target_url / overlay_ip) on the next ClientFor,
			// so a re-activation transparently re-dials with the fresh
			// state. We don't try to be clever and selectively evict
			// only on active=false — the dial cost on a Tailscale
			// overlay is sub-100 ms and the simpler invariant is worth
			// the occasional extra dial.
			n.cache.Evict(p.NodeID)
			n.log.Info("gatewayd: evicted node client cache",
				"node", p.NodeID, "active", p.Active)
		}
	}
}
