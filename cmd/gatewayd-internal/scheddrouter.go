// scheddrouter.go — gatewayd-internal's per-node schedd gRPC client cache
// (Phase 2 / Gate A).
//
// One gatewayd-internal process fronts every app on every node. With N schedd
// peers (one per active compute_node), gatewayd-internal needs to dial the
// schedd that owns the app being woken, not the legacy
// default-local-only socket. This file replaces the single
// scheddgrpc.DialContext(/run/faas/schedd.sock) with a per-node
// cache keyed by compute_nodes.id.
//
// The cache is shape-equivalent to gateway's existing per-node vmmd
// cache (cmd/gatewayd-internal/nodecache.go / pkg/gateway.NodeClientCache):
//
//   - Lazy dial per (node_id) — first lookup opens a conn, subsequent
//     lookups reuse it.
//   - Eviction on compute_node_changed pg_notify payload so a
//     target_url re-write forces a fresh dial on the next call.
//   - Test seam for the subscribe loop, mirroring the vmmd cache
//     pattern (nodecache.go::subscribeFunc).
//
// Why a separate cache from the vmmd cache: the vmmd cache keys by
// node id too, but its `ClientFor` returns a *grpc.ClientConn the
// forwarder consumes per-request; schedd clients have a richer
// surface (Wake / AdmitInstance / ReportActivity / StreamAppLogs /
// StreamWarmHints / ParkInstance) so they live behind typed methods
// on the ScheddClient interface, not a raw *grpc.ClientConn handle.
// A single shared cache would have to either re-export those typed
// methods on the vmmd cache (widens the API surface in a package that
// has nothing to do with schedd) or carry two parallel maps with
// parallel eviction plumbing. The duplicate small cache wins on
// readability — and the gatewayd-internal process has two long-lived gRPC
// clients per node, not one, so the underlying dial cost is
// additive regardless.

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// ScheddDialer is the per-node dial contract. Production wires it
// to scheddgrpc.DialContext (via overlay.Dial for cross-box
// routes); tests inject a fake. tlsCfg is the same mTLS bundle the
// vmmd cache uses (issue #95 / issue #120 / PR #457) so both caches
// present the same trust posture to a remote box.
type ScheddDialer func(ctx context.Context, target string, tlsCfg *tls.Config) (scheddgrpc.ScheddClient, error)

// DefaultScheddDialer is the production dial closure used by
// newScheddRouter. Issue #120 keeps the per-tick dial-cost
// convention: pay the dial cost on every Wake / activity flush /
// log stream and see the truth.
func DefaultScheddDialer(tlsCfg *tls.Config) ScheddDialer {
	return func(ctx context.Context, target string, _ *tls.Config) (scheddgrpc.ScheddClient, error) {
		return scheddgrpc.DialContext(ctx, target, tlsCfg)
	}
}

// ScheddNodeResolver is the read-only slice of state.Store the
// per-node schedd router uses: ComputeNodeByID for the dial target
// and InstanceByID for per-instance dispatch (Phase 2 / Gate A).
// Production wires *state.PgStore; tests inject a fake so the
// router can be exercised without standing up Postgres.
type ScheddNodeResolver interface {
	ComputeNodeByID(ctx context.Context, id string) (state.ComputeNode, error)
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
}

// scheddRouter is gatewayd-internal's per-node schedd client cache. Holds a
// map keyed by compute_nodes.id; values are ScheddClient interfaces
// (production = *scheddgrpc.Client). On any failure to materialise
// a client for a node, the router returns the error to the caller —
// the gateway surfaces 503, and a retry on the next request lands
// on a freshly-evicted entry (or the dial succeeds if the underlying
// transient blip cleared).
//
// Concurrency: the cache map is mutex-guarded. Lookups and inserts
// take RLock / Lock respectively. The dial happens under a
// singleflight key so two concurrent first-lookups for the same
// node collapse to one dial AND so a slow dial to one node does
// NOT block dials to other nodes (the lock is released before
// entering the singleflight call). Eviction takes Lock + closes
// the old client.
//
// Legacy single-box posture: a fleet with only default-local lands
// here with one entry (the synthetic row seeded by migration
// 00083). ScheddForApp / ScheddForInstance resolve that single
// node and behave identically to the pre-PR single-dial path.
type scheddRouter struct {
	store  ScheddNodeResolver
	tlsCfg *tls.Config
	dialer ScheddDialer
	log    *slog.Logger

	mu     sync.Mutex
	cache  map[string]scheddgrpc.ScheddClient
	dialSF singleflight.Group
}

// newScheddRouter wires the production cache. store must be
// non-nil; tlsCfg may be nil for unix-only deployments; dialer may
// be nil and defaults to DefaultScheddDialer. log may be nil
// (slog.Default).
//
// Multi-host safety cluster PR-7 (audit F5): the legacy
// FAAS_SCHEDD_SOCKET override is REMOVED. The router now always
// dials the per-node target from compute_nodes.schedd_target_url.
// Operators who need a non-canonical target for the synthetic
// default-local row must update the row directly; the env-var
// shortcut no longer exists because it would silently swap a
// non-owner box onto a foreign-owned wake in a multi-box fleet.
func newScheddRouter(store ScheddNodeResolver, tlsCfg *tls.Config, dialer ScheddDialer, log *slog.Logger) *scheddRouter {
	if log == nil {
		log = slog.Default()
	}
	if dialer == nil {
		dialer = DefaultScheddDialer(tlsCfg)
	}
	return &scheddRouter{
		store:  store,
		tlsCfg: tlsCfg,
		dialer: dialer,
		log:    log,
		cache:  map[string]scheddgrpc.ScheddClient{},
	}
}

// ScheddForApp resolves the owner schedd for app.NodeID and returns
// its ScheddClient. On the single-box default-local fleet the
// single entry is reused for every app. On a multi-node fleet each
// app lands on exactly one entry.
//
// Returns an error when:
//   - app.NodeID is empty (caller is reading a row that predates
//     migration 00083 — should not happen post-deploy).
//   - the underlying ComputeNodeByID fails or returns ErrNotFound
//     (admin deleted the row mid-flight).
//   - ScheddTargetURL is nil (operator has not configured the
//     per-node schedd dial target yet).
//   - the dial itself fails (transient / permission / network).
func (r *scheddRouter) ScheddForApp(ctx context.Context, app state.App) (scheddgrpc.ScheddClient, error) {
	if app.NodeID == "" {
		return nil, fmt.Errorf("scheddrouter: app %s has empty NodeID (pre-migration or test fixture)", app.ID)
	}
	return r.clientForNode(ctx, app.NodeID)
}

// ScheddForInstance resolves the owner schedd for an instance by
// doing one InstanceByID hop. The handler has the instance id from
// the per-request Target struct; this hop lets the activity flush
// (and any future per-instance gRPC) reach the right schedd without
// a parallel "instance → node_id" cache in the gateway.
//
// NotFound on the instance (parked / never admitted / hard-deleted
// post-M7) returns nil, nil — the caller treats it as a no-op drop,
// matching the pre-PR behaviour where unknown instance ids were
// silently dropped on schedd's side.
func (r *scheddRouter) ScheddForInstance(ctx context.Context, instanceID string) (scheddgrpc.ScheddClient, error) {
	if instanceID == "" {
		return nil, nil
	}
	ins, err := r.store.InstanceByID(ctx, instanceID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("scheddrouter: resolve instance %s: %w", instanceID, err)
	}
	if ins.NodeID == "" {
		return nil, fmt.Errorf("scheddrouter: instance %s has empty NodeID", instanceID)
	}
	return r.clientForNode(ctx, ins.NodeID)
}

// clientForNode is the inner cache + dial step shared by
// ScheddForApp / ScheddForInstance. Looks up the compute_node row
// (so a row mutation / deactivation is visible immediately), reads
// its ScheddTargetURL, and dials on first miss.
//
// Concurrency note: the cache lock is released before the dial so
// a slow handshake to one node does not block fast handshakes to
// other nodes. singleflight (r.dialSF.Do) collapses concurrent
// first-lookups for the SAME node to one dial — without it, a
// fleet where every Wake lands on a freshly-promoted box would
// race N concurrent dials against the same target.
func (r *scheddRouter) clientForNode(ctx context.Context, nodeID string) (scheddgrpc.ScheddClient, error) {
	r.mu.Lock()
	if c, ok := r.cache[nodeID]; ok {
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()

	v, err, _ := r.dialSF.Do(nodeID, func() (any, error) {
		// Re-check inside the singleflight slot too: by the time we
		// land here, another caller may have raced the cache insert.
		r.mu.Lock()
		if c, ok := r.cache[nodeID]; ok {
			r.mu.Unlock()
			return c, nil
		}
		r.mu.Unlock()

		n, err := r.store.ComputeNodeByID(ctx, nodeID)
		if err != nil {
			return nil, fmt.Errorf("scheddrouter: resolve compute_node %s: %w", nodeID, err)
		}
		if n.ScheddTargetURL == nil || *n.ScheddTargetURL == "" {
			return nil, fmt.Errorf("scheddrouter: compute_node %s (%s) has no schedd_target_url configured", n.ID, n.Name)
		}
		target := *n.ScheddTargetURL
		// Multi-host safety cluster PR-7 (audit F5): the legacy
		// FAAS_SCHEDD_SOCKET shortcut is REMOVED. The router always
		// dials the per-node target from
		// compute_nodes.schedd_target_url. Migration 00090 seeds
		// default-local with the canonical production socket;
		// operators who need a non-canonical target must update the
		// row directly. The env-var shortcut existed because the
		// single-box default-local row had no env-driven override
		// path; in a multi-box fleet the shortcut swapped a
		// foreign-owned wake onto the local box silently, which
		// the PR-5 owner gate catches but the shortcut still
		// shouldn't exist.
		c, err := r.dialer(ctx, target, r.tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("scheddrouter: dial schedd for node %s (%s): %w", n.ID, n.Name, err)
		}
		r.mu.Lock()
		// Single-flighted insert: every concurrent caller gets the
		// same c pointer back via v, but the cache map takes Lock
		// here so the insert is atomic w.r.t. Evict.
		r.cache[nodeID] = c
		r.mu.Unlock()
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(scheddgrpc.ScheddClient), nil
}

// Evict drops the cached client for nodeID. Called by the
// compute_node_changed pg_notify subscriber; safe to call for an
// id that's not in the cache. The next ScheddFor{App,Instance}
// call re-reads the row and dials fresh.
func (r *scheddRouter) Evict(nodeID string) {
	r.mu.Lock()
	c, ok := r.cache[nodeID]
	delete(r.cache, nodeID)
	r.mu.Unlock()
	if ok {
		if err := c.Close(); err != nil {
			r.log.Warn("scheddrouter: evict close failed", "node", nodeID, "err", err)
		}
	}
}

// Close releases every cached client. Called once at gatewayd-internal
// shutdown; subsequent ScheddFor{App,Instance} calls return errors
// so in-flight requests see the dial failure rather than a silent
// nil deref.
func (r *scheddRouter) Close() error {
	r.mu.Lock()
	clients := r.cache
	r.cache = map[string]scheddgrpc.ScheddClient{}
	r.mu.Unlock()
	var firstErr error
	for _, c := range clients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WatchNodeChanges subscribes to compute_node_changed pg_notify and
// calls Evict on every payload. Mirrors
// cmd/gatewayd-internal/nodecache.go::WatchEvictions — same db.Subscribe
// wrapper, same malformed-payload drop, same single-goroutine
// shape. The subscribeFunc test seam lives on the type so the
// production wiring is the default and tests can inject a fake.
func (r *scheddRouter) WatchNodeChanges(ctx context.Context, pool *pgxpool.Pool, sub subscribeFunc) {
	if sub == nil {
		sub = db.SubscribeWithReconnect
	}
	notif, err := sub(ctx, pool, []string{db.NotifyComputeNodeChanged}, r.log)
	if err != nil {
		r.log.Error("scheddrouter: subscribe compute_node_changed", "err", err)
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
				r.log.Warn("scheddrouter: bad compute_node_changed payload", "payload", got.Payload)
				continue
			}
			r.Evict(p.NodeID)
			r.log.Info("scheddrouter: evicted schedd client", "node", p.NodeID, "active", p.Active)
		}
	}
}
