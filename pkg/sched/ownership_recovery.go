package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

// RebalanceOrphanedApps runs one bounded ownership recovery batch (ADR-421,
// extending ADR-064). Notifications scope the source before the SQL limit;
// periodic calls use deadNodeID="" and advance an exclusive app-ID cursor.
// Refusals and errors advance that cursor as well, so later, smaller apps can
// recover even when the earliest candidates cannot fit. The apps table is the
// durable work set: restart only resets this paging optimization.
func (e *Engine) RebalanceOrphanedApps(ctx context.Context, deadNodeID string) error {
	if e.ownerNodeID == "" || deadNodeID == e.ownerNodeID {
		return nil
	}
	// A notification burst cannot create a second batch or an unbounded queue
	// behind slow Postgres. Skipped hints are rediscovered by the periodic scan.
	if !e.rebalanceMu.TryLock() {
		return nil
	}
	defer e.rebalanceMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.OwnershipRecoveryTimeoutSeconds)*time.Second)
	defer cancel()
	readCtx, readCancel := ownershipRecoveryStoreContext(ctx)
	node, err := e.store.ComputeNodeByID(readCtx, e.ownerNodeID)
	readCancel()
	if err != nil {
		return fmt.Errorf("sched: ownership recovery: read destination: %w", err)
	}
	if node.Lifecycle != state.NodeLifecycleActive || node.AdmissionCeilingMB <= 0 {
		return nil
	}
	cooldown, limit := e.ownershipRecoveryLimits()
	after := ""
	if deadNodeID == "" {
		after = e.rebalanceCursor
	}
	readCtx, readCancel = ownershipRecoveryStoreContext(ctx)
	apps, err := e.store.ListOrphanedAppsPage(readCtx, cooldown, limit, after, deadNodeID)
	readCancel()
	if err != nil {
		return fmt.Errorf("sched: ownership recovery: list candidates: %w", err)
	}
	if len(apps) == 0 {
		if deadNodeID == "" {
			e.rebalanceCursor = ""
		}
		return nil
	}
	readCtx, readCancel = ownershipRecoveryStoreContext(ctx)
	usedMB, err := e.store.ComputeNodeUsedMB(readCtx, e.ownerNodeID)
	readCancel()
	if err != nil {
		return fmt.Errorf("sched: ownership recovery: read headroom: %w", err)
	}
	for _, app := range apps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if deadNodeID == "" {
			e.rebalanceCursor = app.ID
		}
		neededMB := int64(app.RAMMB) + int64(api.PerVMOverheadMB)
		if app.NodeID == e.ownerNodeID || app.RAMMB <= 0 {
			e.ownershipRecoveryOutcome("no_eligibility")
			continue
		}
		if usedMB+neededMB > int64(node.AdmissionCeilingMB) {
			e.ownershipRecoveryOutcome("no_headroom")
			continue
		}
		transferCtx, transferCancel := ownershipRecoveryStoreContext(ctx)
		err := e.store.ReassignOrphanedAppOwner(transferCtx, app.ID, app.NodeID, e.ownerNodeID, cooldown)
		transferCancel()
		if err != nil {
			if errors.Is(err, state.ErrConflict) {
				e.ownershipRecoveryOutcome("conflict")
			} else {
				e.log.Warn("sched: ownership recovery: transfer failed", "app_id", app.ID,
					"from_node", app.NodeID, "to_node", e.ownerNodeID, "err", err)
			}
			continue
		}
		usedMB += neededMB // conservative soft reservation for the rest of this batch
		e.ownershipRecoveryOutcome("migrated")
		e.ownershipRecoveryTransferred(ctx, app)
	}
	if deadNodeID == "" && len(apps) < limit {
		e.rebalanceCursor = ""
	}
	return nil
}

func ownershipRecoveryStoreContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(api.OwnershipRecoveryStoreTimeoutSeconds)*time.Second)
}

func (e *Engine) ownershipRecoveryLimits() (cooldown, limit int) {
	cooldown, limit = api.RebalanceCooldownSeconds, api.RebalanceMaxPerTickPerNode
	if e.rebalanceCooldownSeconds > 0 {
		cooldown = e.rebalanceCooldownSeconds
	}
	if e.rebalanceMaxPerTick > 0 {
		limit = e.rebalanceMaxPerTick
	}
	return
}

func (e *Engine) ownershipRecoveryOutcome(outcome string) {
	if e.ops != nil {
		e.ops.RebalanceDecisions(outcome).Inc()
	}
}

func (e *Engine) ownershipRecoveryTransferred(ctx context.Context, app state.OrphanedAppCandidate) {
	if e.notif != nil {
		payload := string(safetext.JSONObject(appPlacementChange{
			Kind: "rebalanced", AppID: app.ID, FromNode: app.NodeID, ToNode: e.ownerNodeID,
		}))
		notifyCtx, cancel := ownershipRecoveryStoreContext(ctx)
		err := e.notif.Notify(notifyCtx, db.NotifyAppChanged, payload)
		cancel()
		if err != nil {
			e.log.Warn("sched: ownership recovery: notify routing", "app_id", app.ID, "err", err)
		}
	}
	// Loop's bounded app work pool owns the VM work. Even if it is saturated
	// or not attached yet, ADR-420's durable service sweep rediscovers the new
	// owner without depending on this notification or customer traffic.
	e.mu.Lock()
	submit := e.serviceReconcileSubmit
	e.mu.Unlock()
	if submit != nil {
		submit(ctx, app.ID)
	}
	e.log.Info("sched: ownership recovery: transferred app", "app_id", app.ID,
		"from_node", app.NodeID, "to_node", e.ownerNodeID)
}
