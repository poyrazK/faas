package sched

// adr: 660
import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// Binding changes only retirement identity. Both the row shadow and physical
// hold keep every charge; an observation cannot deduplicate or release capacity.
func (l *NodeLedger) bindRegionalStartupRow(row state.RegionalRuntimeInventoryEntry, ins state.Instance) bool {
	if row.Source == nil || row.Source.Validate() != nil || row.Receipt.Permit != row.Source.Permit || row.Source.Permit.Kind == runtimequota.CapacityAppTask ||
		!row.Resources.Valid() || row.Source.InstanceKey != ins.ID || !state.State(ins.State).CountsForRAM() || !state.RegionalRuntimeAttemptMatches(*row.Source, ins) {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p := row.Source.Permit
	hold, ok := l.regionalHolds[regionalRuntimeHoldKey{p.Grant.Binding.RegionalAppID, p.ID}]
	if !ok || hold.Source == nil || *hold.Source != *row.Source || hold.Resources != row.Resources || hold.ServingSeatsHeld != row.ServingSeatsHeld {
		return false
	}
	// Full-store seeding has completed. An older host epoch or a different
	// lookup spelling with this instance UUID remains a distinct physical hold.
	for key, other := range l.regionalHolds {
		if key != (regionalRuntimeHoldKey{p.Grant.Binding.RegionalAppID, p.ID}) && runtimequota.MatchesID(other.Receipt.Permit.InstanceID, p.InstanceID) {
			return false
		}
	}
	r := l.entries[ins.ID]
	if r == nil || !r.rowShadow || !regionalReservationMatches(*row.Source, Request{Instance: ins.ID, AppID: r.appID, DeploymentID: r.deploymentID, NodeID: r.nodeID, RuntimeRunID: r.runtimeRunID}) ||
		(r.regionalSource != nil && *r.regionalSource != *row.Source) {
		return false
	}
	source := *row.Source
	r.regionalSource, r.regionalResources = &source, row.Resources
	return true
}

// Startup runs before dispatch/recovery workers. Only a fresh authenticated
// native observation can attach a retained source to an existing row shadow.
// Unavailable observations leave conservative charges and existing gates intact.
func (e *Engine) bindOriginalRegionalStartupRows(ctx context.Context) error {
	if _, available := e.vmm.(nativeRuntimeInventoryReader); !available {
		return nil
	}
	var after state.RegionalRuntimeRetirementCursor
	for {
		rows, err := e.store.ListRegionalRuntimeInventory(ctx, "", after, api.RegionalRuntimeRetirementBatch)
		if err != nil || len(rows) > api.RegionalRuntimeRetirementBatch {
			return errors.Join(err, runtimequota.ErrBinding)
		}
		if len(rows) == 0 {
			return ctx.Err()
		}
		for _, row := range rows {
			p := row.Receipt.Permit
			next := state.RegionalRuntimeRetirementCursor{AppID: p.Grant.Binding.RegionalAppID, PermitID: p.ID}
			if p.Validate() != nil || next.AppID < after.AppID || next.AppID == after.AppID && next.PermitID <= after.PermitID {
				return runtimequota.ErrBinding
			}
			after = next
		}
		_, _, err = e.observeRegionalRuntimeRowsWithMatch(ctx, after, rows, func(row state.RegionalRuntimeInventoryEntry, sample fcvm.NativeRuntimeInventorySample) error {
			if !nativeStartupSourceUnambiguous(sample.Inventory) || row.Source.Permit.Kind == runtimequota.CapacityAppTask {
				return nil
			}
			ins, err := e.store.InstanceByID(ctx, row.Source.InstanceKey)
			if errors.Is(err, state.ErrNotFound) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("sched: original startup row: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			e.ledger.bindRegionalStartupRow(row, ins)
			return nil
		})
		if err != nil {
			return err
		}
	}
}

// Extra historical owners or independent managed tasks cannot establish an
// unambiguous original relation. This check grants no inventory completeness.
func nativeStartupSourceUnambiguous(inventory fcvm.NativeRuntimeInventory) bool {
	if len(inventory.Unresolved) != 0 || len(inventory.HelperTasks) != 0 {
		return false
	}
	for _, owner := range inventory.Owners {
		if owner.Unresolved != "" || owner.OriginalSource == nil || owner.OriginalSource.Validate() != nil || owner.Placement == nil || owner.OriginalSource.Permit != owner.Placement.Permit || owner.RegionalRuntimeCompatibility() != "" {
			return false
		}
	}
	for _, task := range inventory.ManagedUIDTasks {
		matches := 0
		for _, owner := range inventory.Owners {
			if task.PID == owner.PID && task.StartTime == owner.StartTime && task.UID == owner.Lease.UID {
				matches++
			}
		}
		if matches != 1 {
			return false
		}
	}
	return true
}
