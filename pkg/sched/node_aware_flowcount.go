package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// NodeAwareFlowCounter prefers the compute-side conntrack count transported
// in NodeTelemetryCache and falls back to the local reader for single-box
// deployments or during a telemetry gap. This keeps the reaper's existing
// fail-open contract while making the split-box decision based on the node
// that actually owns the VM.
type NodeAwareFlowCounter struct {
	telemetry *NodeTelemetryCache
	fallback  FlowCounter
	now       func() time.Time
}

// NewNodeAwareFlowCounter composes the remote telemetry cache with the
// existing local FlowCounter. A nil fallback is valid and returns zero on a
// cache miss, matching the legacy no-op behavior.
func NewNodeAwareFlowCounter(telemetry *NodeTelemetryCache, fallback FlowCounter) *NodeAwareFlowCounter {
	return &NodeAwareFlowCounter{telemetry: telemetry, fallback: fallback, now: time.Now}
}

// Warm primes the local reader only for instances that lack fresh compute-side
// telemetry. Split-box deployments normally have a complete ReportCapacity
// batch, so they avoid an unnecessary conntrack walk on the control plane.
func (c *NodeAwareFlowCounter) Warm(ctx context.Context, instances []state.Instance) error {
	if c == nil || c.fallback == nil {
		return nil
	}
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	covered := make(map[string]struct{})
	for _, row := range c.telemetry.Snapshot(now) {
		covered[row.Telemetry.InstanceID] = struct{}{}
	}
	missing := make([]state.Instance, 0, len(instances))
	for _, instance := range instances {
		if _, ok := covered[instance.ID]; !ok {
			missing = append(missing, instance)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	warmer, ok := c.fallback.(interface {
		Warm(context.Context, []state.Instance) error
	})
	if !ok {
		return nil
	}
	return warmer.Warm(ctx, missing)
}

// Open returns remote compute telemetry when fresh, then delegates to the
// local fallback. Both paths retain the reaper's fail-open behavior.
func (c *NodeAwareFlowCounter) Open(ctx context.Context, instanceID string) (int64, error) {
	if c == nil {
		return 0, nil
	}
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if value, ok := c.telemetry.LookupOpenConns(instanceID, now); ok {
		return value, nil
	}
	if c.fallback != nil {
		return c.fallback.Open(ctx, instanceID)
	}
	return 0, nil
}
