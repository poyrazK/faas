package sched

// adr: 636
import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type regionalRuntimeHoldKey struct{ app, permit string }

func (l *NodeLedger) beginRegionalRecovery() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.regionalAdmissionBarrierLocked(); err != nil {
		return err
	}
	l.seedInProgress = true
	return nil
}

func (l *NodeLedger) finishRegionalRecovery(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seedInProgress = false
	if err != nil {
		l.recoveryBarrier = errors.Join(ErrRegionalRuntimeRecoveryUnresolved, err)
	}
}

func (l *NodeLedger) regionalAdmissionBarrierLocked() error {
	if l.recoveryBarrier != nil {
		return l.recoveryBarrier
	}
	if l.seedInProgress {
		return ErrRegionalRuntimeRecoveryUnresolved
	}
	return nil
}

// Inventory holds have their own identities. A row/run tuple cannot prove its
// host incarnation, so mutable instance rows never merge or replace these holds.
func (l *NodeLedger) retainRegionalInventory(entry state.RegionalRuntimeInventoryEntry, ceiling int) error {
	p := entry.Receipt.Permit
	if p.Validate() != nil || entry.Receipt.IssuedAt == nil || entry.Receipt.ReleasedAt != nil || entry.ServingSeatsHeld < 0 ||
		(entry.Source != nil && (entry.Source.Validate() != nil || entry.Source.Permit != p)) {
		return runtimequota.ErrBinding
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := regionalRuntimeHoldKey{p.Grant.Binding.RegionalAppID, p.ID}
	if old, exists := l.regionalHolds[key]; exists {
		if old.Receipt.Permit != p || old.Resources != entry.Resources || old.ServingSeatsHeld != entry.ServingSeatsHeld ||
			(old.Source == nil) != (entry.Source == nil) || (old.Source != nil && *old.Source != *entry.Source) {
			return runtimequota.ErrBinding
		}
		return regionalInventoryProvenanceError(old) // A retry never double-charges an original permit.
	}
	if entry.Resources != (state.RegionalRuntimeResources{}) && !entry.Resources.Valid() {
		return runtimequota.ErrBinding
	}
	r := entry.Resources
	ram := 0
	if r.Valid() {
		ram = r.RAMMB + api.PerVMOverheadMB
	}
	n := l.resident[p.NodeID]
	if n == nil {
		n = &nodeReservation{ceilingMB: api.RAMAdmissionCeilingMB}
	}
	maxInt := int(^uint(0) >> 1)
	if entry.ServingSeatsHeld > int64(maxInt-l.perApp[key.app]) ||
		entry.ServingSeatsHeld > int64(maxInt-l.perAppDeployment[key.app+"\x00"+p.DeploymentID]) ||
		n.residentRAM > maxInt-ram || n.usedVCPU > maxInt-r.VCPU || n.usedCPUMillicores > maxInt-r.CPUMillicores {
		return fmt.Errorf("sched: regional inventory accounting overflow: %w", ErrRegionalRuntimeRecoveryUnresolved)
	}
	if ceiling > 0 {
		n.ceilingMB = ceiling
	}
	l.resident[p.NodeID] = n
	n.residentRAM += ram
	n.usedVCPU += r.VCPU
	n.usedCPUMillicores += r.CPUMillicores // Original peak has no invented expiry.
	l.perApp[key.app] += int(entry.ServingSeatsHeld)
	l.perAppDeployment[key.app+"\x00"+p.DeploymentID] += int(entry.ServingSeatsHeld)
	if entry.Source != nil {
		source := *entry.Source
		entry.Source = &source
	}
	l.regionalHolds[key] = entry
	return regionalInventoryProvenanceError(entry)
}

func regionalInventoryProvenanceError(entry state.RegionalRuntimeInventoryEntry) error {
	if !entry.Resources.Valid() {
		return fmt.Errorf("sched: original resource provenance missing: %w", ErrRegionalRuntimeRecoveryUnresolved)
	}
	if entry.Source == nil {
		return fmt.Errorf("sched: original native lookup source missing: %w", ErrRegionalRuntimeRecoveryUnresolved)
	}
	return nil
}

// Only a caller with accepted native stop evidence may invoke this release.
func (l *NodeLedger) releaseRegionalInventoryLocked(source runtimequota.LaunchSource) bool {
	p := source.Permit
	key := regionalRuntimeHoldKey{p.Grant.Binding.RegionalAppID, p.ID}
	hold, ok := l.regionalHolds[key]
	if !ok || hold.Source == nil || *hold.Source != source {
		return false
	}
	delete(l.regionalHolds, key)
	if n := l.resident[p.NodeID]; n != nil {
		if hold.Resources.Valid() {
			n.residentRAM -= hold.Resources.RAMMB + api.PerVMOverheadMB
		}
		n.usedVCPU -= hold.Resources.VCPU
		n.usedCPUMillicores -= hold.Resources.CPUMillicores
		if n.residentRAM == 0 && n.usedVCPU == 0 && n.usedCPUMillicores == 0 {
			delete(l.resident, p.NodeID)
		}
	}
	l.perApp[key.app] -= int(hold.ServingSeatsHeld)
	l.cleanupApp(key.app)
	depKey := key.app + "\x00" + p.DeploymentID
	l.perAppDeployment[depKey] -= int(hold.ServingSeatsHeld)
	if l.perAppDeployment[depKey] == 0 {
		delete(l.perAppDeployment, depKey)
	}
	return true
}

func regionalReservationMatches(source runtimequota.LaunchSource, r Request) bool {
	p := source.Permit
	return source.Validate() == nil && source.InstanceKey == r.Instance && runtimequota.MatchesID(p.RunID, r.RuntimeRunID) &&
		runtimequota.MatchesID(p.Grant.Binding.RegionalAppID, r.AppID) && runtimequota.MatchesID(p.DeploymentID, r.DeploymentID) && runtimequota.MatchesID(p.NodeID, r.NodeID)
}

// Binding occurs at the captured dispatch boundary, before any RPC. It cannot
// reconstruct a full permit from an old row during startup or retirement.
func (l *NodeLedger) bindOriginalRegionalRuntime(source runtimequota.LaunchSource, resources state.RegionalRuntimeResources) error {
	if source.Validate() != nil || !resources.Valid() {
		return runtimequota.ErrBinding
	}
	if l == nil {
		return nil
	} // Explicit bookkeeping-only Engine fixtures.
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.entries[source.InstanceKey]
	if r == nil || r.rowShadow {
		return runtimequota.ErrBinding
	}
	if !regionalReservationMatches(source, Request{Instance: source.InstanceKey, AppID: r.appID, DeploymentID: r.deploymentID, NodeID: r.nodeID, RuntimeRunID: r.runtimeRunID}) ||
		r.plan != source.Permit.Grant.Policy.Plan || !regionalReservationKindMatches(r.kind, source.Permit.Kind) || (r.regionalSource != nil && *r.regionalSource != source) {
		return runtimequota.ErrBinding
	}
	if r.admissionMB != resources.RAMMB+api.PerVMOverheadMB || r.vcpu != resources.VCPU || r.cpuMillicores+r.cpuBoostMillicores != resources.CPUMillicores ||
		(r.regionalResources != (state.RegionalRuntimeResources{}) && r.regionalResources != resources) {
		return runtimequota.ErrBinding
	}
	copy := source
	r.regionalSource = &copy
	r.regionalResources = resources
	return nil
}

func regionalReservationKindMatches(kind Kind, purpose string) bool {
	switch kind {
	case KindWake:
		return purpose == runtimequota.CapacityServing
	case KindWarmPool:
		return purpose == runtimequota.CapacityWarm
	case KindSnapshotPrime:
		return purpose == runtimequota.CapacityPrime
	case KindAppTask:
		return purpose == runtimequota.CapacityAppTask
	default:
		return false
	}
}

func (e *Engine) seedRegionalRuntimeInventory(ctx context.Context) error {
	var after state.RegionalRuntimeRetirementCursor
	var result error
	ceilings := map[string]int{}
	for {
		// Empty cell is deliberate: signer replacement cannot hide old holds.
		rows, err := e.store.ListRegionalRuntimeInventory(ctx, "", after, api.RegionalRuntimeRetirementBatch)
		if err != nil {
			return errors.Join(result, fmt.Errorf("sched: seed full regional inventory: %w", err))
		}
		if len(rows) > api.RegionalRuntimeRetirementBatch {
			return errors.Join(result, fmt.Errorf("sched: oversized regional inventory page: %w", runtimequota.ErrBinding))
		}
		if len(rows) == 0 {
			return errors.Join(result, ctx.Err())
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return errors.Join(result, err)
			}
			p := row.Receipt.Permit
			next := state.RegionalRuntimeRetirementCursor{AppID: p.Grant.Binding.RegionalAppID, PermitID: p.ID}
			if next.AppID < after.AppID || (next.AppID == after.AppID && next.PermitID <= after.PermitID) {
				return errors.Join(result, fmt.Errorf("sched: nonadvancing regional inventory page: %w", runtimequota.ErrBinding))
			}
			ceiling, ok := ceilings[p.NodeID]
			if !ok {
				node, err := e.store.ComputeNodeByID(ctx, p.NodeID)
				if err != nil || !runtimequota.MatchesID(p.NodeID, node.ID) || node.AdmissionCeilingMB <= 0 {
					result = errors.Join(result, fmt.Errorf("sched: unresolved original regional node %s: %w", p.NodeID, errors.Join(ErrRegionalRuntimeRecoveryUnresolved, err)))
				} else {
					ceiling = node.AdmissionCeilingMB
				}
				ceilings[p.NodeID] = ceiling
			}
			if err := e.ledger.retainRegionalInventory(row, ceiling); err != nil {
				result = errors.Join(result, fmt.Errorf("sched: seed original regional permit %s/%s: %w", next.AppID, next.PermitID, err))
			}
			after = next
		}
	}
}
