package gateway

// adr: 20261102081618 §5 — an app on several nodes, with Cloudflare entering
// at any node.
//
// A compute edge forwards each request to the gatewayd-internal of a node the
// app runs on. With local first, that gateway serves the request from its own
// node's instances while one has a free slot, and names every node of the app
// in the response so the edge can spread the app's requests by instance
// count. Each VM then takes edge traffic through its own node's gateway, so
// the per-instance limit holds, and a request crosses between nodes at most
// once. Scale-out estimates the app's whole demand from this gateway's share.

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// maxInstanceNodes bounds InstanceNodesHeader.
const maxInstanceNodes = 16

// NodeCount is how many ready instances of an app run on one node.
type NodeCount struct {
	NodeID string
	Ready  int
}

// nodeSpreadBackend is the backend view local first needs.
type nodeSpreadBackend interface {
	// ReadyNodeCounts returns the app's ready instances per node across its
	// live deployments, sorted by node id.
	ReadyNodeCounts(appID string) []NodeCount
	// LocalReadyTargets returns deploymentID's ready instances on nodeID,
	// starting at a rotating offset.
	LocalReadyTargets(appID, deploymentID, nodeID string) []Target
}

type edgeLocalFirst struct {
	localNode func(context.Context) (string, error)
}

// WithEdgeLocalFirst turns on local first for requests a compute edge
// forwarded (FAAS_GATEWAY_EDGE_LOCAL_FIRST). localNode returns this node's
// compute_nodes id; while it fails, requests keep the global rotation.
func (h *Handler) WithEdgeLocalFirst(localNode func(context.Context) (string, error)) *Handler {
	if localNode != nil {
		h.edgeLocalFirst = &edgeLocalFirst{localNode: localNode}
	}
	return h
}

func (h *Handler) spreadBackend() (nodeSpreadBackend, bool) {
	if h == nil || h.edgeLocalFirst == nil {
		return nil, false
	}
	b, ok := h.backend.(nodeSpreadBackend)
	return b, ok
}

func (h *Handler) localNode(ctx context.Context) string {
	id, err := h.edgeLocalFirst.localNode(ctx)
	if err != nil {
		return ""
	}
	return id
}

type localFirstKey struct{}

// localFirstContext marks ctx for local first when a compute edge forwarded
// the request and this node's id is known.
func (h *Handler) localFirstContext(ctx context.Context) context.Context {
	if _, ok := h.spreadBackend(); !ok || !edgeAskedPlacement(ctx) {
		return ctx
	}
	if node := h.localNode(ctx); node != "" {
		return context.WithValue(ctx, localFirstKey{}, node)
	}
	return ctx
}

// acquireLocalTarget takes a slot on one of this node's ready instances of the
// picked deployment, if the request is marked for local first and one is free.
func (h *Handler) acquireLocalTarget(ctx context.Context, app App, pick PickResult, perVM int) (PickResult, func(), bool) {
	node, _ := ctx.Value(localFirstKey{}).(string)
	backend, ok := h.spreadBackend()
	if node == "" || !ok || pick.Target.DeploymentID == "" {
		return PickResult{}, nil, false
	}
	for _, t := range backend.LocalReadyTargets(app.ID, pick.Target.DeploymentID, node) {
		if release, ok := h.vmConcurrency.tryAcquire(t.InstanceID, string(app.Plan), perVM); ok {
			local := pick
			local.Target = t
			return local, release, true
		}
	}
	return PickResult{}, nil, false
}

// instanceNodesContext attaches every node of the app for InstanceNodesHeader
// when a compute edge asked.
func (h *Handler) instanceNodesContext(ctx context.Context, appID string) context.Context {
	backend, ok := h.spreadBackend()
	if !ok || !edgeAskedPlacement(ctx) {
		return ctx
	}
	if value := formatNodeCounts(backend.ReadyNodeCounts(appID)); value != "" {
		return withInstanceNodes(ctx, value)
	}
	return ctx
}

// formatNodeCounts renders InstanceNodesHeader: "<node id>=<count>,...",
// sorted, at most maxInstanceNodes entries.
func formatNodeCounts(counts []NodeCount) string {
	counts = append([]NodeCount(nil), counts...)
	sort.Slice(counts, func(i, j int) bool { return counts[i].NodeID < counts[j].NodeID })
	parts := make([]string, 0, min(len(counts), maxInstanceNodes))
	for _, c := range counts {
		if c.NodeID == "" || c.Ready <= 0 {
			continue
		}
		if len(parts) == maxInstanceNodes {
			break
		}
		parts = append(parts, c.NodeID+"="+strconv.Itoa(c.Ready))
	}
	return strings.Join(parts, ",")
}

// scaleByLocalShare turns the instances this gateway's own traffic needs into
// the app's whole demand: with the edges spreading an app's requests by
// instance count, this gateway sees the share its node's instances hold. A
// gateway with no instance of the app on its node keeps desired as it is.
func (h *Handler) scaleByLocalShare(ctx context.Context, appID string, desired, maxInstances int) int {
	backend, ok := h.spreadBackend()
	if !ok || desired <= 0 {
		return desired
	}
	node := h.localNode(ctx)
	if node == "" {
		return desired
	}
	local, total := 0, 0
	for _, c := range backend.ReadyNodeCounts(appID) {
		total += c.Ready
		if c.NodeID == node {
			local = c.Ready
		}
	}
	if local == 0 || total <= local {
		return desired
	}
	scaled := (desired*total + local - 1) / local
	if maxInstances > 0 && scaled > maxInstances {
		scaled = maxInstances
	}
	return scaled
}
