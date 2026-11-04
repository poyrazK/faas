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
	return c.UpdateEgressCircuitRevision(ctx, appID, netns.EgressCircuitSnapshot{Targets: targets})
}

func (c *VMMClient) UpdateEgressCircuitRevision(ctx context.Context, appID string, snapshot netns.EgressCircuitSnapshot) error {
	targets, err := netns.CanonicalEgressCircuitTargets(snapshot.Targets)
	if err != nil {
		return err
	}
	wire := make([]*vmmdpb.EgressCircuitTarget, 0, len(targets))
	for _, t := range targets {
		wire = append(wire, &vmmdpb.EgressCircuitTarget{
			Addr: t.Addr.String(),
			Port: uint32(t.Port),
		})
	}
	ack, err := c.cli.UpdateEgressCircuit(ctx, &vmmdpb.UpdateEgressCircuitRequest{
		AppId:    appID,
		Circuits: wire,
		Revision: snapshot.Revision,
	})
	if err != nil {
		return liftErr(err)
	}
	if snapshot.Revision > 0 && ack.GetRevision() < snapshot.Revision {
		return fmt.Errorf("vmm client: node did not acknowledge circuit revision")
	}
	return nil
}

func (r *VMMRouter) UpdateEgressCircuitRevision(ctx context.Context, nodeID, appID string, snapshot netns.EgressCircuitSnapshot) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	updater, ok := cli.(interface {
		UpdateEgressCircuitRevision(context.Context, string, netns.EgressCircuitSnapshot) error
	})
	if !ok {
		return fmt.Errorf("vmm router: durable egress circuits unsupported by node %q", nodeID)
	}
	return updater.UpdateEgressCircuitRevision(ctx, appID, snapshot)
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
// It commits the whole-app union to the desired store before fanout because
// the vmmd RPC replaces the complete set. Retrying the same revision repairs
// missed pushes and lets boot-time readers recover the same policy.
type RoutedEgressCircuitApplier struct {
	router  EgressCircuitRouter
	nodes   EgressCircuitNodeLister
	desired EgressCircuitDesiredStore
}

type EgressCircuitDesiredStore interface {
	PutAppEgressCircuits(context.Context, string, []netns.EgressCircuitTarget) (netns.EgressCircuitSnapshot, error)
}

// WithDesiredStore enables commit-before-fanout semantics. Wake-time readers
// on new nodes and after daemon restart use the same authoritative revision.
func (a *RoutedEgressCircuitApplier) WithDesiredStore(store EgressCircuitDesiredStore) *RoutedEgressCircuitApplier {
	a.desired = store
	return a
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
	snapshot := netns.EgressCircuitSnapshot{Targets: targets}
	if a.desired != nil {
		var err error
		snapshot, err = a.desired.PutAppEgressCircuits(ctx, appID, targets)
		if err != nil {
			return err
		}
	}
	return a.applySnapshot(ctx, appID, snapshot)
}

func (a *RoutedEgressCircuitApplier) applySnapshot(ctx context.Context, appID string, snapshot netns.EgressCircuitSnapshot) error {
	nodeIDs, err := a.nodes(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: egress circuit: list nodes for app %s: %w", appID, err)
	}
	var firstErr error
	for _, nodeID := range nodeIDs {
		if nodeID == "" {
			continue
		}
		var err error
		if snapshot.Revision > 0 {
			updater, ok := a.router.(interface {
				UpdateEgressCircuitRevision(context.Context, string, string, netns.EgressCircuitSnapshot) error
			})
			if !ok {
				err = fmt.Errorf("durable egress circuit updates unsupported")
			} else {
				err = updater.UpdateEgressCircuitRevision(ctx, nodeID, appID, snapshot)
			}
		} else {
			err = a.router.UpdateEgressCircuit(ctx, nodeID, appID, snapshot.Targets)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sched: egress circuit: node %s: %w", nodeID, err)
		}
	}
	return firstErr
}
