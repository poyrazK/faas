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
type computeGatewayPool struct {
	store computeGatewayNodeLister
	log   *slog.Logger

	mu        sync.Mutex
	endpoints []computeGatewayEndpoint
	refreshed time.Time
	next      uint64
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
	if time.Since(p.refreshed) < computeGatewayPoolTTL {
		out := append([]computeGatewayEndpoint(nil), p.endpoints...)
		p.mu.Unlock()
		return out, nil
	}
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
	p.endpoints = endpoints
	p.refreshed = time.Now()
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
	notif, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyComputeNodeChanged}, p.log)
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
			var payload struct {
				NodeID string `json:"node_id"`
				Active bool   `json:"active"`
			}
			if err := json.Unmarshal([]byte(got.Payload), &payload); err != nil || payload.NodeID == "" {
				p.log.Warn("compute_gateway_pool: bad compute_node_changed payload", "payload", got.Payload)
				continue
			}
			// Drop the cached snapshot; the next Dial re-reads.
			p.mu.Lock()
			p.refreshed = time.Time{}
			p.mu.Unlock()
			p.log.Debug("compute_gateway_pool: evicted snapshot",
				"node_id", payload.NodeID, "active", payload.Active)
		}
	}
}
