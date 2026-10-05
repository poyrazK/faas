package fcvm

// adr: 629
import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
	"github.com/onebox-faas/faas/pkg/state"
)

func newNativeLaunchRecord(lease Lease, generation, bootID string, p *runtimequota.Placement) nativeLaunchRecord {
	r := nativeLaunchRecord{Version: 1, Generation: generation, KernelBootID: bootID, Lease: lease}
	if p != nil {
		r.Version, r.RegionalRuntime = 2, cloneRuntimePlacement(p)
	}
	return r
}

func sameOptionalRuntimePlacement(a, b *runtimequota.Placement) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Permit == b.Permit && (a.Seat == nil && b.Seat == nil || a.Seat != nil && b.Seat != nil && *a.Seat == *b.Seat)
}

func sameNativeRegionalBinding(a, b nativeLaunchRecord) bool {
	return a.Version == b.Version && sameOptionalRuntimePlacement(a.RegionalRuntime, b.RegionalRuntime) && (a.RegionalRuntime == nil || sameNativePhysicalLease(a.Lease, b.Lease))
}

func validateNativeRegionalPlacement(lease Lease, p *runtimequota.Placement) error {
	if p == nil {
		return nil
	}
	if p.Validate() != nil || !runtimequota.MatchesID(p.Permit.InstanceID, lease.Instance) || p.Permit.Grant.Policy.Plan != lease.Plan || lease.IsBuilder || lease.Networkless {
		return runtimequotahost.ErrBinding
	}
	if p.Permit.Kind == runtimequota.CapacityServing && p.Seat == nil || p.Permit.Kind != runtimequota.CapacityServing && p.Permit.Kind != runtimequota.CapacityWarm && p.Seat != nil {
		return runtimequotahost.ErrBinding
	}
	return nil
}

type nativeRegionalLeaseVMM interface {
	prepareNativeRegionalLease(context.Context, Lease, runtimequota.Placement) error
}

// Production regional launches require native ownership even when the old
// lifecycle is configured. Portable VMMs retain their separate test surface.
func (m *Manager) prepareNativeRuntime(ctx context.Context, lease Lease, p *runtimequota.Placement) error {
	if p != nil {
		if v, ok := m.vmm.(nativeRegionalLeaseVMM); ok {
			return v.prepareNativeRegionalLease(ctx, lease, p.Clone())
		}
		if m.nativeVMM() != nil {
			return runtimequotahost.ErrStorage
		}
	}
	return m.prepareNativeLease(ctx, lease)
}

func (v *JailerVMM) prepareNativeRegionalLease(ctx context.Context, lease Lease, p runtimequota.Placement) error {
	if v.nativeRecovery == nil {
		return runtimequotahost.ErrStorage
	}
	return v.prepareNativeLeaseWithPlacement(ctx, lease, &p)
}

// This joins durable physical provenance, never current execution authority or
// native pause/stop proof. A later serving seat keeps the initial placement.
func (m *Manager) checkNativeRegionalInstance(inst *Instance, p runtimequota.Placement) error {
	v := m.nativeVMM()
	if v == nil {
		return nil
	}
	r := v.nativeRecoveryRuntime()
	owner, err := r.journal.read(inst.Lease.Instance)
	if err != nil {
		return err
	}
	if inst.nativeGeneration == "" || owner.Generation != inst.nativeGeneration || r.generation(inst.Lease.Instance) != owner.Generation || owner.RegionalRuntime == nil || owner.RegionalRuntime.Permit != p.Permit || !sameNativePhysicalLease(owner.Lease, inst.Lease) || !liveNativeSnapshotOwner(owner) {
		return runtimequotahost.ErrBinding
	}
	return nil
}

// Recovery keeps fenced and paused ownership. No serving map, seat release or
// allocator reuse can be inferred from a matching bookkeeping record.
func (m *Manager) checkNativeRegionalRecovery(ctx context.Context, v nativeRecoveryVMM) error {
	records, err := v.nativeRecoveryRuntime().journal.records(ctx)
	if err != nil {
		return err
	}
	if m.runtimeHostJournal == nil {
		for _, r := range records {
			if r.RegionalRuntime != nil {
				return runtimequotahost.ErrStorage
			}
		}
		return nil
	}
	permits, err := m.runtimeHostJournal.PermitInventory()
	if err != nil {
		return err
	}
	host := m.runtimeHostJournal.Identity()
	byInstance := make(map[string]bool, len(permits))
	for _, p := range permits {
		if p.Registered && p.Permit.HostEpochID == host.EpochID {
			byInstance[p.Permit.InstanceID] = true
		}
	}
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.RegionalRuntime != nil {
			p := r.RegionalRuntime.Permit
			if err := m.runtimeHostJournal.CheckRetainedPlacement(*r.RegionalRuntime); err != nil {
				return fmt.Errorf("native regional recovery: original placement is not retained: %w", err)
			}
			if p.NodeID != host.NodeID || p.HostEpochID != host.EpochID || r.KernelBootID != host.BootID {
				return fmt.Errorf("native regional recovery: physical binding is not retained: %w", runtimequotahost.ErrBinding)
			}
			continue
		}
		id, err := uuid.Parse(r.Lease.Instance)
		if err == nil && !r.ResourcesRemoved && byInstance[id.String()] {
			return fmt.Errorf("native regional recovery: legacy frame lacks original permit: %w", runtimequotahost.ErrBinding)
		}
	}
	return ctx.Err()
}

type snapshotCapturePreflighter interface {
	preflightSnapshotKeepAlive(context.Context, Lease, SnapshotSpec) error
}

func (v *JailerVMM) preflightSnapshotKeepAlive(_ context.Context, _ Lease, spec SnapshotSpec) error {
	if v.nativeRecovery != nil || spec.RegionalMigrationSource != nil {
		return fmt.Errorf("native recovery: snapshot publication producer is unavailable: %w", state.ErrConflict)
	}
	return nil
}

// Refuse known unsupported producers before retaining a migration source or
// fencing its serving seat. The capture boundary repeats this preflight.
func (m *Manager) preflightRegionalMigrationCapture(ctx context.Context, inst *Instance, spec SnapshotSpec) error {
	if v, ok := m.vmm.(snapshotCapturePreflighter); ok {
		if err := v.preflightSnapshotKeepAlive(ctx, inst.Lease, spec); err != nil {
			return err
		}
	} else if m.nativeVMM() != nil {
		return errors.Join(state.ErrConflict, errors.New("native regional capture preflight is unavailable"))
	}
	return m.checkNativeRegionalInstance(inst, *spec.RegionalMigrationSource)
}
