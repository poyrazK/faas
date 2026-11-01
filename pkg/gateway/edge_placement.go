package gateway

// adr: 20261031105055 — the compute edge routes each app host to the node
// that last served it.
//
// production-us 2026-10-31: Cloudflare sent almost every tunnel request to
// one node's connector, so that node's gatewayd-internal routed the whole
// cell's traffic. gatewayd-internal names the node of the instance it
// forwarded to, and the edge sends the host's next requests there.

import (
	"context"
	"net/http"
	"strings"
)

const (
	// EdgePlacementHeader is a compute edge's request for an
	// InstanceNodeHeader answer. Every gatewayd-public removes a client's
	// copy; gatewayd-internal moves it to the request context.
	EdgePlacementHeader = "X-Faas-Edge-Placement"
	// InstanceNodeHeader names the compute_nodes.id of the instance a
	// response came from. The public edge always strips it;
	// gatewayd-internal drops a guest's copy.
	InstanceNodeHeader = "X-Faas-Instance-Node"
)

type edgePlacementKey struct{}

// takeEdgePlacement moves the edge's placement request from the headers to
// the request context, so no later hop forwards it.
func takeEdgePlacement(r *http.Request) *http.Request {
	asked := strings.TrimSpace(r.Header.Get(EdgePlacementHeader)) == "1"
	r.Header.Del(EdgePlacementHeader)
	if !asked {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), edgePlacementKey{}, true))
}

// stampInstanceNode names the instance's node on a forwarded response when
// the edge asked for it.
func stampInstanceNode(ctx context.Context, h http.Header, nodeID string) {
	if asked, _ := ctx.Value(edgePlacementKey{}).(bool); asked && nodeID != "" {
		h.Set(InstanceNodeHeader, nodeID)
	}
}

type preferredNodeKey struct{}

type placementAskedKey struct{}

// WithPlacementAsked marks ctx as a request the edge looked up a node for,
// so a dialer counts a miss for it and not for its own health probes.
func WithPlacementAsked(ctx context.Context) context.Context {
	return context.WithValue(ctx, placementAskedKey{}, true)
}

// PlacementAsked reports whether WithPlacementAsked marked ctx.
func PlacementAsked(ctx context.Context) bool {
	asked, _ := ctx.Value(placementAskedKey{}).(bool)
	return asked
}

// PreferredNode is the node a compute edge wants a request routed to, and the
// host it learned that for.
type PreferredNode struct {
	NodeID string
	Host   string
}

// WithPreferredNode attaches the edge's routing preference for the dialer.
func WithPreferredNode(ctx context.Context, pref PreferredNode) context.Context {
	return context.WithValue(ctx, preferredNodeKey{}, pref)
}

// PreferredNodeFrom returns the preference WithPreferredNode attached.
func PreferredNodeFrom(ctx context.Context) (PreferredNode, bool) {
	pref, ok := ctx.Value(preferredNodeKey{}).(PreferredNode)
	return pref, ok && pref.NodeID != ""
}
