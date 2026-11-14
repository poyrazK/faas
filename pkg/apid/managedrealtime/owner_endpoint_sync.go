package managedrealtime

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// The endpoint reconcile registers every endpoint on every node every 30
// seconds. A registration a node already holds is skipped (ADR-1323,
// amendment of 2026-11-03): the node listed the endpoint in this pass's
// inventory, and this apid sent it the same fingerprint within
// endpointRegistrationRefresh. A node that restarted without its snapshot
// lists nothing, so everything is sent to it again.
const (
	endpointRegistrationRefresh = 10 * time.Minute
	endpointInventoryFresh      = 2 * time.Minute
	activeNodesCacheTTL         = 5 * time.Second
)

type sentRegistration struct {
	fingerprint string
	at          time.Time
}

type endpointRegistrations struct {
	mu        sync.Mutex
	nodes     []state.ComputeNode
	nodesAt   time.Time
	present   map[string]map[string]struct{}
	presentAt map[string]time.Time
	sent      map[string]map[string]sentRegistration
	// previews is each node's last reported realtime previews; a nil list
	// is a node that predates the report.
	previews   map[string][]string
	previewsAt map[string]time.Time
}

// nodePreviewLister is a node operator that also reports which realtime
// previews the node serves (realtime.NodePreviewsHeader).
type nodePreviewLister interface {
	EndpointsWithPreviews(context.Context) ([]string, []string, error)
}

func (r *endpointRegistrations) recordPreviews(nodeID string, previews []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.previews == nil {
		r.previews, r.previewsAt = map[string][]string{}, map[string]time.Time{}
	}
	r.previews[nodeID], r.previewsAt[nodeID] = previews, time.Now()
}

// NodesServePreview reports whether the realtime fleet serves a preview
// (realtime.NodePreview*): false once any node that answered the inventory
// in the last endpointInventoryFresh lists its previews without it. A node
// that predates the report, or has not answered lately, does not count, so
// a fleet of older nodes keeps the decision to apid's flags as before
// (ADR 20261021080640, amendment of 2026-11-09).
func (o *leasedRealtimeOwner) NodesServePreview(name string) bool {
	r := &o.endpointSync
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for node, previews := range r.previews {
		if previews == nil || now.Sub(r.previewsAt[node]) >= endpointInventoryFresh {
			continue
		}
		if !slices.Contains(previews, name) {
			return false
		}
	}
	return true
}

// activeNodes lists active nodes, reusing a list read in the last few
// seconds: one reconcile pass registers every endpoint against the same list.
func (o *leasedRealtimeOwner) activeNodes(ctx context.Context) ([]state.ComputeNode, error) {
	r := &o.endpointSync
	r.mu.Lock()
	if r.nodes != nil && time.Since(r.nodesAt) < activeNodesCacheTTL {
		nodes := r.nodes
		r.mu.Unlock()
		return nodes, nil
	}
	r.mu.Unlock()
	nodes, err := o.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.nodes, r.nodesAt = nodes, time.Now()
	r.mu.Unlock()
	return nodes, nil
}

func (r *endpointRegistrations) recordInventory(nodeID string, ids []string) {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.present == nil {
		r.present, r.presentAt = map[string]map[string]struct{}{}, map[string]time.Time{}
	}
	r.present[nodeID], r.presentAt[nodeID] = set, time.Now()
}

func (r *endpointRegistrations) unchanged(nodeID, endpointID, fingerprint string, now time.Time) bool {
	if fingerprint == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sent, ok := r.sent[nodeID][endpointID]
	if !ok || sent.fingerprint != fingerprint || now.Sub(sent.at) >= endpointRegistrationRefresh {
		return false
	}
	if now.Sub(r.presentAt[nodeID]) >= endpointInventoryFresh {
		return false
	}
	_, present := r.present[nodeID][endpointID]
	return present
}

func (r *endpointRegistrations) recordSent(nodeID, endpointID, fingerprint string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent == nil {
		r.sent = map[string]map[string]sentRegistration{}
	}
	if r.sent[nodeID] == nil {
		r.sent[nodeID] = map[string]sentRegistration{}
	}
	r.sent[nodeID][endpointID] = sentRegistration{fingerprint: fingerprint, at: now}
	// The node holds the endpoint now, whatever its last inventory said.
	if set := r.present[nodeID]; set != nil {
		set[endpointID] = struct{}{}
	}
}

func (r *endpointRegistrations) forget(endpointID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sent := range r.sent {
		delete(sent, endpointID)
	}
	for _, set := range r.present {
		delete(set, endpointID)
	}
}

// registerEndpointOnNodes sends endpoint to every active node that does not
// already hold it.
func (o *leasedRealtimeOwner) registerEndpointOnNodes(ctx context.Context, endpoint realtime.Endpoint) error {
	now := time.Now()
	fingerprint := realtime.EndpointFingerprint(endpoint, now)
	return o.broadcastEndpointNodes(ctx, func(node state.ComputeNode, op RealtimeNodeOperator) (bool, error) {
		if o.endpointSync.unchanged(node.ID, endpoint.ID, fingerprint, now) {
			return false, nil
		}
		if err := op.RegisterEndpoint(ctx, endpoint); err != nil {
			return true, err
		}
		o.endpointSync.recordSent(node.ID, endpoint.ID, fingerprint, now)
		return true, nil
	})
}
