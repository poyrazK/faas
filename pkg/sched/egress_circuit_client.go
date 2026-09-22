package sched

// Client + router plumbing for the ADR-201 §3 egress breaker, and the
// production EgressCircuitApplier that joins the breaker to vmmd.

import (
	"context"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/netns"
)

// UpdateEgressCircuit pushes the complete open-circuit set for an app to one
// vmmd. An empty set closes every circuit.
func (c *VMMClient) UpdateEgressCircuit(ctx context.Context, appID string, targets []netns.EgressCircuitTarget) error {
	wire := make([]*vmmdpb.EgressCircuitTarget, 0, len(targets))
	for _, t := range targets {
		if !t.Valid() {
			continue
		}
		wire = append(wire, &vmmdpb.EgressCircuitTarget{
			Addr: t.Addr.String(),
			Port: uint32(t.Port),
		})
	}
	if _, err := c.cli.UpdateEgressCircuit(ctx, &vmmdpb.UpdateEgressCircuitRequest{
		AppId:    appID,
		Circuits: wire,
	}); err != nil {
		return liftErr(err)
	}
	return nil
}

// UpdateEgressCircuit routes a circuit-set push to the vmmd owning the node.
// Kept off RoutedVMM for the same reason as UpdatePrivateNetwork: existing
// scheduler fakes must keep compiling, so callers opt into the capability
// through a narrow assertion.
func (r *VMMRouter) UpdateEgressCircuit(ctx context.Context, nodeID, appID string, targets []netns.EgressCircuitTarget) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	updater, ok := cli.(interface {
		UpdateEgressCircuit(context.Context, string, []netns.EgressCircuitTarget) error
	})
	if !ok {
		return fmt.Errorf("vmm router: egress circuit update unsupported by node %q", nodeID)
	}
	return updater.UpdateEgressCircuit(ctx, appID, targets)
}

// EgressCircuitNodeLister reports the nodes currently holding live instances
// of an app. The breaker needs it because a circuit is per-app state that has
// to reach every node the app is running on, and an app can be spread across
// the fleet.
type EgressCircuitNodeLister func(ctx context.Context, appID string) ([]string, error)

// EgressCircuitRouter is the narrow slice of VMMRouter the applier uses.
type EgressCircuitRouter interface {
	UpdateEgressCircuit(ctx context.Context, nodeID, appID string, targets []netns.EgressCircuitTarget) error
}

// RoutedEgressCircuitApplier is the production EgressCircuitApplier: it fans a
// circuit-set push out to every node running the app.
//
// It holds the desired set per app rather than per (app, upstream), because
// the vmmd RPC takes a whole set. A per-upstream transition therefore has to
// re-push the union of that app's open circuits, which is also what makes the
// push idempotent and self-healing.
type RoutedEgressCircuitApplier struct {
	router EgressCircuitRouter
	nodes  EgressCircuitNodeLister
}

// NewRoutedEgressCircuitApplier wires the applier. A nil router or lister
// makes every call a no-op, which is the report-only posture.
func NewRoutedEgressCircuitApplier(router EgressCircuitRouter, nodes EgressCircuitNodeLister) *RoutedEgressCircuitApplier {
	return &RoutedEgressCircuitApplier{router: router, nodes: nodes}
}

// ApplyEgressCircuits pushes the app's complete open-circuit set to every node
// holding one of its live instances.
//
// A node that reports an error does NOT abort the fan-out: the remaining nodes
// still converge, and the first error is returned so the breaker can retry on
// the next probe. Aborting would leave the fleet split — some nodes enforcing,
// some not — which is harder to reason about during an incident than a uniform
// retry.
func (a *RoutedEgressCircuitApplier) ApplyEgressCircuits(ctx context.Context, appID string, targets []netns.EgressCircuitTarget) error {
	if a == nil || a.router == nil || a.nodes == nil {
		return nil
	}
	nodeIDs, err := a.nodes(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: egress circuit: list nodes for app %s: %w", appID, err)
	}
	var firstErr error
	for _, nodeID := range nodeIDs {
		if nodeID == "" {
			continue
		}
		if err := a.router.UpdateEgressCircuit(ctx, nodeID, appID, targets); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sched: egress circuit: node %s: %w", nodeID, err)
		}
	}
	return firstErr
}
