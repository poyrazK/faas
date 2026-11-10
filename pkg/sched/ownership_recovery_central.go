// adr: 20261110210000 — with FAAS_APP_OWNER_AUTHORITY=control-plane the
// control plane's schedd alone assigns app owners, including after a node
// dies; compute schedds no longer pull orphaned apps onto themselves.
package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// RebalanceOrphanedAppsCentral is the control plane's ownership recovery
// (ADR-20261110210000 §4). RebalanceOrphanedApps runs in every node-bound
// schedd and moves orphaned apps onto its own node, so a node chooses which
// apps it receives. Here the destination is chosen by the control plane with
// ChooseOwnerNode, the balance ClaimUnplaced uses for a new app, among the
// other admitting nodes with headroom for one instance. It has the same
// RebalancerHandle shape (deadNodeID "" is the periodic sweep), paging,
// cooldown and outcomes.
func (e *Engine) RebalanceOrphanedAppsCentral(ctx context.Context, deadNodeID string) error {
	reader, ok := e.store.(state.AppOwnershipLoadReader)
	if !ok {
		return errors.New("sched: central ownership recovery: store cannot report ownership load")
	}
	if !e.rebalanceMu.TryLock() {
		return nil
	}
	defer e.rebalanceMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.OwnershipRecoveryTimeoutSeconds)*time.Second)
	defer cancel()

	cooldown, limit := e.ownershipRecoveryLimits()
	after := ""
	if deadNodeID == "" {
		after = e.rebalanceCursor
	}
	readCtx, readCancel := ownershipRecoveryStoreContext(ctx)
	apps, err := e.store.ListOrphanedAppsPage(readCtx, cooldown, limit, after, deadNodeID)
	readCancel()
	if err != nil {
		return fmt.Errorf("sched: central ownership recovery: list candidates: %w", err)
	}
	if len(apps) == 0 {
		if deadNodeID == "" {
			e.rebalanceCursor = ""
		}
		return nil
	}
	readCtx, readCancel = ownershipRecoveryStoreContext(ctx)
	nodes, err := e.store.ActiveComputeNodes(readCtx)
	readCancel()
	if err != nil {
		return fmt.Errorf("sched: central ownership recovery: list nodes: %w", err)
	}
	readCtx, readCancel = ownershipRecoveryStoreContext(ctx)
	owned, err := reader.OwnedAppRAMByNode(readCtx)
	readCancel()
	if err != nil {
		return fmt.Errorf("sched: central ownership recovery: read ownership load: %w", err)
	}
	// Headroom per node, read once per batch and reserved as apps move.
	used := map[string]int64{}
	usedOf := func(nodeID string) (int64, error) {
		if mb, ok := used[nodeID]; ok {
			return mb, nil
		}
		readCtx, readCancel := ownershipRecoveryStoreContext(ctx)
		mb, err := e.store.ComputeNodeUsedMB(readCtx, nodeID)
		readCancel()
		if err != nil {
			return 0, err
		}
		used[nodeID] = mb
		return mb, nil
	}

	for _, app := range apps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if deadNodeID == "" {
			e.rebalanceCursor = app.ID
		}
		if app.RAMMB <= 0 {
			e.ownershipRecoveryOutcome("no_eligibility")
			continue
		}
		neededMB := int64(app.RAMMB) + int64(api.PerVMOverheadMB)
		candidates := make([]state.ComputeNode, 0, len(nodes))
		for _, n := range nodes {
			if n.ID == app.NodeID || n.Lifecycle != state.NodeLifecycleActive {
				continue
			}
			mb, err := usedOf(n.ID)
			if err != nil {
				e.log.Warn("sched: central ownership recovery: read headroom", "node", n.ID, "err", err)
				continue
			}
			if mb+neededMB > int64(n.AdmissionCeilingMB) {
				continue
			}
			candidates = append(candidates, n)
		}
		dest, ok := ChooseOwnerNode(candidates, owned, app.RAMMB)
		if !ok {
			e.ownershipRecoveryOutcome("no_headroom")
			continue
		}
		transferCtx, transferCancel := ownershipRecoveryStoreContext(ctx)
		err := e.store.ReassignOrphanedAppOwner(transferCtx, app.ID, app.NodeID, dest.ID, cooldown)
		transferCancel()
		if err != nil {
			if errors.Is(err, state.ErrConflict) {
				e.ownershipRecoveryOutcome("conflict")
			} else {
				e.log.Warn("sched: central ownership recovery: transfer failed", "app_id", app.ID,
					"from_node", app.NodeID, "to_node", dest.ID, "err", err)
			}
			continue
		}
		used[dest.ID] += neededMB
		owned[dest.ID] += int64(app.RAMMB)
		e.ownershipRecoveryOutcome("migrated")
		e.ownershipRecoveryTransferred(ctx, app, dest.ID)
	}
	if deadNodeID == "" && len(apps) < limit {
		e.rebalanceCursor = ""
	}
	return nil
}
