package main

// adr: 20261031105055 — the compute edge routes each app host to the node
// that last served it (see pkg/gateway/edge_placement.go).

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

const (
	// edgePlacementRoutingEnv turns on placement routing for a compute edge.
	edgePlacementRoutingEnv = "FAAS_EDGE_PLACEMENT_ROUTING"
	// edgePlacementTTL is how long a host keeps its node after the last
	// response that named it.
	edgePlacementTTL = 30 * time.Second
	// edgePlacementMaxHosts bounds the map. Only forwarded responses name a
	// node, so it holds at most one entry per app.
	edgePlacementMaxHosts = 50_000
)

// Results of gatewayd_public_edge_placement_total, per dial attempt.
const (
	placementRemote   = "remote"
	placementSelf     = "self"
	placementMiss     = "miss"
	placementUnusable = "unusable"
	placementFailed   = "failed"
)

var placementResults = []string{placementRemote, placementSelf, placementMiss, placementUnusable, placementFailed}

// edgePlacement remembers, per app host, the compute node that last served
// it (gateway.EdgePlacement).
type edgePlacement struct {
	ttl time.Duration
	max int
	now func() time.Time

	mu    sync.Mutex
	hosts map[string]placementEntry
}

type placementEntry struct {
	node    string
	expires time.Time
}

func newEdgePlacement() *edgePlacement {
	return &edgePlacement{ttl: edgePlacementTTL, max: edgePlacementMaxHosts, now: time.Now, hosts: map[string]placementEntry{}}
}

// placementHost is r's host as the map keys it: lower case, no port, no
// trailing dot.
func placementHost(r *http.Request) string {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.Host), "."))
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	return host
}

// Prefer returns the node r's host was last served from, while it is fresh.
func (e *edgePlacement) Prefer(r *http.Request) (gateway.PreferredNode, bool) {
	host := placementHost(r)
	e.mu.Lock()
	defer e.mu.Unlock()
	entry, ok := e.hosts[host]
	if !ok {
		return gateway.PreferredNode{}, false
	}
	if !e.now().Before(entry.expires) {
		delete(e.hosts, host)
		return gateway.PreferredNode{}, false
	}
	return gateway.PreferredNode{NodeID: entry.node, Host: host}, true
}

// Learn records the node gatewayd-internal named for r's response.
func (e *edgePlacement) Learn(r *http.Request, nodeID string) {
	host := placementHost(r)
	if host == "" || nodeID == "" {
		return
	}
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.hosts[host]; !ok && len(e.hosts) >= e.max {
		e.evictLocked(now)
	}
	e.hosts[host] = placementEntry{node: nodeID, expires: now.Add(e.ttl)}
}

// Forget drops host's entry, after its node refused a dial.
func (e *edgePlacement) Forget(host string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.hosts, host)
}

// evictLocked makes room for one entry: expired entries first, then any.
// The caller holds e.mu.
func (e *edgePlacement) evictLocked(now time.Time) {
	for host, entry := range e.hosts {
		if !now.Before(entry.expires) {
			delete(e.hosts, host)
		}
	}
	for host := range e.hosts {
		if len(e.hosts) < e.max {
			return
		}
		delete(e.hosts, host)
	}
}
