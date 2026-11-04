package main

// adr: 20261031105055 — the compute edge routes each app host to the node
// that last served it (see pkg/gateway/edge_placement.go).

import (
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

const (
	// edgePlacementRoutingEnv turns on placement routing for a compute edge.
	edgePlacementRoutingEnv = "FAAS_EDGE_PLACEMENT_ROUTING"
	// edgePlacementNodeSetEnv makes placement routing spread a host's
	// requests over every node the app runs on (ADR-20261102081618 §5b).
	edgePlacementNodeSetEnv = "FAAS_EDGE_PLACEMENT_NODE_SET"
	// edgePlacementMaxNodes bounds one host's node set, as
	// gatewayd-internal bounds the header.
	edgePlacementMaxNodes = 16
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
// it (gateway.EdgePlacement). With the node set on it remembers every node of
// the app and how many ready instances each holds, and spreads the host's
// requests by those counts (gateway.EdgePlacementSet).
type edgePlacement struct {
	ttl time.Duration
	max int
	now func() time.Time
	// spread turns on the node set; pick draws a share in [0, n).
	spread bool
	pick   func(n int) int

	mu    sync.Mutex
	hosts map[string]placementEntry
}

type placementEntry struct {
	node    string
	nodes   []nodeShare
	total   int
	expires time.Time
}

// nodeShare is one node of a host's set and its ready instances.
type nodeShare struct {
	id    string
	ready int
}

func newEdgePlacement() *edgePlacement {
	return &edgePlacement{ttl: edgePlacementTTL, max: edgePlacementMaxHosts, now: time.Now, pick: rand.IntN, hosts: map[string]placementEntry{}}
}

// withNodeSet turns on spreading by node set (FAAS_EDGE_PLACEMENT_NODE_SET).
func (e *edgePlacement) withNodeSet() *edgePlacement {
	e.spread = true
	return e
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
	if entry.total > 0 {
		draw := e.pick(entry.total)
		for _, n := range entry.nodes {
			if draw < n.ready {
				return gateway.PreferredNode{NodeID: n.id, Host: host}, true
			}
			draw -= n.ready
		}
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

// LearnSet records every node gatewayd-internal named for r's response,
// "<node id>=<ready instances>,...". Without the node set, or with no usable
// entry, it changes nothing.
func (e *edgePlacement) LearnSet(r *http.Request, raw string) {
	host := placementHost(r)
	if !e.spread || host == "" {
		return
	}
	var nodes []nodeShare
	total := 0
	for _, part := range strings.Split(raw, ",") {
		id, count, ok := strings.Cut(strings.TrimSpace(part), "=")
		ready, err := strconv.Atoi(count)
		if !ok || id == "" || err != nil || ready <= 0 {
			continue
		}
		if len(nodes) == edgePlacementMaxNodes {
			break
		}
		nodes = append(nodes, nodeShare{id: id, ready: ready})
		total += ready
	}
	if len(nodes) == 0 {
		return
	}
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.hosts[host]; !ok && len(e.hosts) >= e.max {
		e.evictLocked(now)
	}
	e.hosts[host] = placementEntry{node: nodes[0].id, nodes: nodes, total: total, expires: now.Add(e.ttl)}
}

// Forget drops node from host's entry after it refused a dial: from the node
// set when there is one, else the whole entry.
func (e *edgePlacement) Forget(host, node string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	entry, ok := e.hosts[host]
	if !ok {
		return
	}
	kept := entry.nodes[:0:0]
	total := 0
	for _, n := range entry.nodes {
		if n.id != node {
			kept = append(kept, n)
			total += n.ready
		}
	}
	if len(kept) == 0 {
		delete(e.hosts, host)
		return
	}
	entry.nodes, entry.total, entry.node = kept, total, kept[0].id
	e.hosts[host] = entry
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
