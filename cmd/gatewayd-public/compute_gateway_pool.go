package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

const computeGatewayPoolTTL = 5 * time.Second
const computeGatewayDialTimeout = 2 * time.Second

// computeGatewayPoolStaleTTL bounds how long the last successful inventory
// keeps routing traffic while the registry cannot be read (ADR-1344 §5 rule
// 1). It matches the gateway's stale route tier (ADR-190). A stale entry is
// cheap to keep: a node removed during the outage fails its dial and the
// pool moves on to the next one.
const computeGatewayPoolStaleTTL = 10 * time.Minute

// computeGatewayRefreshTimeout bounds one background registry read so a hung
// database cannot pin the refresh goroutine.
const computeGatewayRefreshTimeout = 5 * time.Second

// computeGatewayStaleLogEvery limits the "serving last-known inventory"
// warning during an outage.
const computeGatewayStaleLogEvery = time.Minute

// computeGatewayNodeLister is intentionally narrower than state.Store. The
// public edge only needs the active node inventory and must not become coupled
// to the rest of the control-plane store surface.
type computeGatewayNodeLister interface {
	ActiveComputeNodes(context.Context) ([]state.ComputeNode, error)
}

type computeGatewayEndpoint struct {
	name    string
	address string
}

// computeGatewayPool is the split-box data-plane dialer. It is refreshed from
// the control-plane registry, so adding or draining a compute node does not
// require changing the public gateway unit or restarting the edge.
//
// The pool deliberately opens a new connection for each request. The
// InternalReverseProxy transport otherwise pools an idle connection under one
// logical URL, which could keep sending traffic to a node after the registry
// removed it. The small connection setup cost is the safe default until the
// transport grows per-node connection keys.
//
// A registry failure never turns into a data-plane failure while a recent
// inventory exists (ADR-1344 §5): once the TTL expires, requests keep using
// the last successful inventory and one background read refreshes it.
type computeGatewayPool struct {
	store computeGatewayNodeLister
	log   *slog.Logger

	mu        sync.Mutex
	endpoints []computeGatewayEndpoint
	// refreshed is the TTL stamp: zero forces the next dial to re-read.
	refreshed time.Time
	// loadedAt is when endpoints last came from a successful registry read.
	loadedAt time.Time
	// invalidated is set by compute_node_changed and makes the next dial
	// re-read synchronously instead of serving the cached inventory.
	invalidated bool
	// generation counts invalidations so a read that started before one
	// cannot clear it.
	generation   uint64
	refreshing   bool
	lastStaleLog time.Time
	next         uint64
}

func newComputeGatewayPool(store computeGatewayNodeLister, log *slog.Logger) gateway.InternalDialer {
	if log == nil {
		log = slog.Default()
	}
	return &computeGatewayPool{store: store, log: log}
}

func (p *computeGatewayPool) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	endpoints, err := p.snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: refresh compute gateway inventory: %w", gateway.ErrNoComputeCapacity, err)
	}
	if len(endpoints) == 0 {
		return nil, gateway.ErrNoComputeCapacity
	}

	start := p.nextIndex(len(endpoints))
	var lastErr error
	for offset := range endpoints {
		endpoint := endpoints[(start+offset)%len(endpoints)]
		dialCtx, cancel := context.WithTimeout(ctx, computeGatewayDialTimeout)
		conn, dialErr := (&net.Dialer{}).DialContext(dialCtx, "tcp", endpoint.address)
		cancel()
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
		p.log.Warn("compute gateway unavailable; trying next node",
			"node", endpoint.name, "target", endpoint.address, "err", dialErr)
	}
	if lastErr == nil {
		lastErr = errors.New("all compute gateway dials failed")
	}
	return nil, fmt.Errorf("%w: %w", gateway.ErrNoComputeCapacity, lastErr)
}

func (p *computeGatewayPool) nextIndex(size int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := int(p.next % uint64(size))
	p.next++
	return index
}

func (p *computeGatewayPool) snapshot(ctx context.Context) ([]computeGatewayEndpoint, error) {
	p.mu.Lock()
	now := time.Now()
	if !p.invalidated && now.Sub(p.refreshed) < computeGatewayPoolTTL {
		out := append([]computeGatewayEndpoint(nil), p.endpoints...)
		p.mu.Unlock()
		return out, nil
	}
	if !p.invalidated && p.usableLocked(now) {
		// The TTL expired but the inventory is recent: never make a request
		// wait on the registry. One background read refreshes it.
		out := append([]computeGatewayEndpoint(nil), p.endpoints...)
		if !p.refreshing {
			p.refreshing = true
			go p.refreshInBackground(ctx)
		}
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()

	// First dial, an invalidation, or an inventory older than the stale
	// bound: read the registry now.
	out, err := p.refresh(ctx)
	if err == nil {
		return out, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.usableLocked(time.Now()) {
		// Serve the last-known inventory and retry once per TTL through
		// the background path, so requests stop waiting on a registry that
		// is down. The next successful read still picks up whatever the
		// invalidation announced.
		p.invalidated = false
		p.refreshed = time.Now()
		p.logStaleLocked(err)
		return append([]computeGatewayEndpoint(nil), p.endpoints...), nil
	}
	return nil, err
}

// usableLocked reports whether the last successful inventory is recent
// enough to route on. The caller holds p.mu.
func (p *computeGatewayPool) usableLocked(now time.Time) bool {
	return !p.loadedAt.IsZero() && now.Sub(p.loadedAt) <= computeGatewayPoolStaleTTL
}

// logStaleLocked records that the registry failed and the last-known
// inventory is serving, at most once per computeGatewayStaleLogEvery. The
// caller holds p.mu.
func (p *computeGatewayPool) logStaleLocked(err error) {
	now := time.Now()
	if now.Sub(p.lastStaleLog) < computeGatewayStaleLogEvery {
		return
	}
	p.lastStaleLog = now
	p.log.Warn("compute gateway registry unavailable; serving last-known inventory",
		"age", now.Sub(p.loadedAt).Round(time.Second), "nodes", len(p.endpoints), "err", err)
}

// refreshInBackground re-reads the registry for a pool that is serving a
// recent inventory. On failure it waits one TTL before the next attempt, so
// an outage costs one registry read per TTL rather than one per request.
// The read outlives the request that started it, so it keeps parent's values
// but not its cancellation.
func (p *computeGatewayPool) refreshInBackground(parent context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), computeGatewayRefreshTimeout)
	defer cancel()
	_, err := p.refresh(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshing = false
	if err != nil {
		if !p.invalidated {
			p.refreshed = time.Now()
		}
		p.logStaleLocked(err)
	}
}

// invalidate drops the cached inventory so the next dial re-reads the
// registry before routing.
func (p *computeGatewayPool) invalidate() {
	p.mu.Lock()
	p.refreshed = time.Time{}
	p.invalidated = true
	p.generation++
	p.mu.Unlock()
}

// refresh reads the registry and, on success, replaces the inventory.
func (p *computeGatewayPool) refresh(ctx context.Context) ([]computeGatewayEndpoint, error) {
	p.mu.Lock()
	generation := p.generation
	p.mu.Unlock()

	nodes, err := p.store.ActiveComputeNodes(ctx)
	if err != nil {
		return nil, err
	}
	endpoints := make([]computeGatewayEndpoint, 0, len(nodes))
	for _, node := range nodes {
		if !node.Active || node.GatewayTargetURL == nil {
			continue
		}
		address, ok := parseComputeGatewayTarget(*node.GatewayTargetURL)
		if !ok {
			p.log.Warn("ignoring invalid compute gateway target", "node", node.Name, "target", *node.GatewayTargetURL)
			continue
		}
		endpoints = append(endpoints, computeGatewayEndpoint{name: node.Name, address: address})
	}

	p.mu.Lock()
	now := time.Now()
	p.endpoints = endpoints
	p.loadedAt = now
	// A read that began before an invalidation may predate the change it
	// announced: keep the result, but leave the pool invalidated so the
	// next dial reads again.
	if generation == p.generation {
		p.refreshed = now
		p.invalidated = false
	}
	out := append([]computeGatewayEndpoint(nil), p.endpoints...)
	p.mu.Unlock()
	return out, nil
}

func parseComputeGatewayTarget(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "tcp" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", false
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return "", false
	}
	return u.Host, true
}

// WatchInvalidations subscribes to the compute_node_changed
// pg_notify channel and drops the cached endpoint snapshot on
// every mutation. Without this, a node drain / activation /
// overlay-IP change only refreshes on the next Dial after
// computeGatewayPoolTTL elapses (5s stale + 2s dial = up to
// 7s of stale routes). pg_notify collapses that to <500ms.
//
// Workstream B / issue #1184 / Task #65 / ADR-137. Mirrors the
// gatewayd-internal nodecache.WatchEvictions pattern
// (cmd/gatewayd-internal/nodecache.go:243). The 5th pg_notify
// consumer on this channel (after router_watcher, nodekeys,
// rebalancer, live_migrator) is bounded by the existing
// compute_node_changed payload format; no schema impact.
//
// The subscriber exits cleanly on ctx cancel; pgxpool's
// SubscribeWithReconnect handles Postgres restarts (the inner
// loop reconnects). A nil pool makes WatchInvalidations a
// no-op so test fixtures can opt out without rewriting the
// wiring path.
//
// Payload format: db.Notification{Channel, Payload}. Payload is
// JSON {"node_id":"<uuid>", "active":true|false}; the active
// flag is informational — any mutation evicts because the
// resolver re-reads the row on the next Dial. Bad payloads log
// Warn and are dropped (consistent with the internal gateway).
func (p *computeGatewayPool) WatchInvalidations(ctx context.Context, pool *pgxpool.Pool) {
	if pool == nil {
		p.log.Warn("compute_gateway_pool: WatchInvalidations called with nil pool; no-op")
		return
	}
	notif, err := db.SubscribeWithResync(ctx, pool, []string{db.NotifyComputeNodeChanged}, p.log)
	if err != nil {
		p.log.Error("compute_gateway_pool: subscribe compute_node_changed", "err", err)
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
			if got.Channel == db.ResyncChannel {
				// Node changes may have been missed while LISTEN was
				// down (ADR-1344 §5 rule 3): re-read before routing.
				p.invalidate()
				continue
			}
			var payload struct {
				NodeID string `json:"node_id"`
				Active bool   `json:"active"`
			}
			if err := json.Unmarshal([]byte(got.Payload), &payload); err != nil || payload.NodeID == "" {
				p.log.Warn("compute_gateway_pool: bad compute_node_changed payload", "payload", got.Payload)
				continue
			}
			// Drop the cached snapshot; the next Dial re-reads.
			p.invalidate()
			p.log.Debug("compute_gateway_pool: evicted snapshot",
				"node_id", payload.NodeID, "active", payload.Active)
		}
	}
}
