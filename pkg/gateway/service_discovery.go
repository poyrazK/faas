package gateway

import (
	"context"
	"sort"
)

// ServiceEndpoint is the routable identity of one running service replica.
//
// NodeID is the compute node identity used by the gateway's per-node transport;
// it is intentionally not a network address. A future guest-side proxy can
// resolve that identity through the node registry without exposing control
// plane addresses to workloads. Port is the effective workload HTTP port;
// legacy targets that carry zero use the vmmd default of 8080.
type ServiceEndpoint struct {
	InstanceID   string `json:"instance_id"`
	NodeID       string `json:"node_id"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Port         int    `json:"port"`
}

// ServiceEndpointsSnapshot is the immutable, point-in-time endpoint view for
// one app. The slice is freshly allocated on every read, so callers may retain
// or mutate it without racing the gateway picker.
type ServiceEndpointsSnapshot struct {
	AppID     string            `json:"app_id"`
	Endpoints []ServiceEndpoint `json:"endpoints"`
}

// ServiceEndpointProvider is the narrow control-plane seam used by the
// loopback service-discovery reader. Implementations must return only currently
// routable instances and may reconcile their local cache before taking the
// snapshot.
type ServiceEndpointProvider interface {
	ServiceEndpoints(ctx context.Context, appID string) (ServiceEndpointsSnapshot, error)
}

// ServiceEndpoints returns a deterministic snapshot of the app's currently
// routable targets. The gateway picker is authoritative for request routing, so
// this projection follows the same lifecycle: RecordTarget publishes running
// replicas and EvictInstance removes terminal ones. An empty local cache is
// reconciled from the optional live-target loader before the snapshot is read;
// this makes the control surface useful immediately after a gateway restart.
func (b *PGBackend) ServiceEndpoints(ctx context.Context, appID string) (ServiceEndpointsSnapshot, error) {
	snapshot := ServiceEndpointsSnapshot{AppID: appID, Endpoints: []ServiceEndpoint{}}
	if b == nil || appID == "" {
		return snapshot, nil
	}
	if b.HealthyCount(appID) == 0 {
		if err := b.ReconcileLiveTargets(ctx, appID); err != nil {
			return snapshot, err
		}
	}

	b.tgtMu.RLock()
	defer b.tgtMu.RUnlock()
	picker := b.appsPicker[appID]
	if picker == nil {
		return snapshot, nil
	}

	// A target should only occur once in the picker, but a deployment
	// transition can briefly leave the same instance visible in two buckets.
	// Select a deterministic representative before sorting the final list so
	// map iteration order can never change the wire snapshot.
	byInstance := make(map[string]ServiceEndpoint)
	for _, set := range picker.sets {
		for _, target := range set.entries {
			if target.InstanceID == "" || target.NodeID == "" {
				continue
			}
			deploymentID := target.DeploymentID
			if deploymentID == "_legacy" {
				deploymentID = ""
			}
			port := target.Port
			if port == 0 {
				port = 8080
			}
			candidate := ServiceEndpoint{
				InstanceID:   target.InstanceID,
				NodeID:       target.NodeID,
				DeploymentID: deploymentID,
				Port:         port,
			}
			current, exists := byInstance[target.InstanceID]
			if !exists || serviceEndpointLess(candidate, current) {
				byInstance[target.InstanceID] = candidate
			}
		}
	}
	for _, endpoint := range byInstance {
		snapshot.Endpoints = append(snapshot.Endpoints, endpoint)
	}
	sort.Slice(snapshot.Endpoints, func(i, j int) bool {
		return serviceEndpointLess(snapshot.Endpoints[i], snapshot.Endpoints[j])
	})
	return snapshot, nil
}

func serviceEndpointLess(a, b ServiceEndpoint) bool {
	if a.NodeID != b.NodeID {
		return a.NodeID < b.NodeID
	}
	if a.InstanceID != b.InstanceID {
		return a.InstanceID < b.InstanceID
	}
	if a.DeploymentID != b.DeploymentID {
		return a.DeploymentID < b.DeploymentID
	}
	return a.Port < b.Port
}

var _ ServiceEndpointProvider = (*PGBackend)(nil)
