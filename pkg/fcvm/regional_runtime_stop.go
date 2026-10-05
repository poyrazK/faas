package fcvm

// adr: 630
import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

// RetireRegionalRuntime closes the original host permit and retires its exact
// native incarnation. The unsigned result is a host assertion for the cell
// owner, never a signature or an automatic ledger release. Missing provenance,
// historical boots and uncertain teardown retain the physical quota hold.
func (m *Manager) RetireRegionalRuntime(ctx context.Context, instance string, p runtimequota.CapacityPermit, fenceID string) (runtimequota.CapacityStop, error) {
	if m == nil || m.runtimeHostJournal == nil || m.nativeVMM() == nil {
		return runtimequota.CapacityStop{}, runtimequotahost.ErrStorage
	}
	r := m.nativeVMM().nativeRecoveryRuntime()
	if r.journal == nil || r.resources == nil {
		return runtimequota.CapacityStop{}, runtimequotahost.ErrStorage
	}
	if err := ctx.Err(); err != nil {
		return runtimequota.CapacityStop{}, err
	}
	host := m.runtimeHostJournal.Identity()
	if p.Validate() != nil || !runtimequota.MatchesID(p.InstanceID, instance) || p.NodeID != host.NodeID || p.HostEpochID != host.EpochID {
		return runtimequota.CapacityStop{}, runtimequotahost.ErrBinding
	}
	if err := m.runtimeHostJournal.CheckRetainedPlacement(runtimequota.Placement{Permit: p}); err != nil {
		return runtimequota.CapacityStop{}, err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return runtimequota.CapacityStop{}, err
	}
	// Fencing excludes a late launch or resume before joining the instance
	// flight. No current app row or execution admission is needed to stop.
	fence, err := m.runtimeHostJournal.FencePermit(p, fenceID)
	if err != nil {
		return runtimequota.CapacityStop{}, err
	}
	if err := m.retireRegionalNative(ctx, instance, p); err != nil {
		return runtimequota.CapacityStop{}, err
	}
	if err := m.runtimeHostJournal.WaitPermit(ctx, p); err != nil {
		return runtimequota.CapacityStop{}, err
	}
	if err := m.verifyRegionalNativeStop(ctx, instance, p); err != nil {
		return runtimequota.CapacityStop{}, err
	}
	// Recheck the healthy durable fence after all native observations. A
	// poisoned/closed host owner must not advertise a capacity stop.
	current, err := m.runtimeHostJournal.FencePermit(p, fence.FenceID)
	if err != nil || current != fence {
		return runtimequota.CapacityStop{}, errors.Join(err, runtimequotahost.ErrBinding)
	}
	if err := ctx.Err(); err != nil {
		return runtimequota.CapacityStop{}, err
	}
	return runtimequota.CapacityStop{Permit: p, FenceID: fence.FenceID, AdmissionFrozen: true}, nil
}

// Use the same teardown flight as ordinary Destroy. Join boot/resume before
// resolving the original native record; a not-yet-published boot is included.
// Cleanup uses that full physical lease, never a mutable scheduler row.
func (m *Manager) retireRegionalNative(ctx context.Context, instance string, p runtimequota.CapacityPermit) (result error) {
	flight, owner, err := m.beginInstanceTeardown(ctx, instance, "")
	if err != nil {
		return err
	}
	if !owner {
		return flight.err // The caller still owes fresh native verification.
	}
	defer func() { m.finishInstanceTeardown(instance, flight, -1, false, false, result) }()
	if err := m.joinForTeardown(ctx, instance); err != nil {
		return err
	}
	v := m.nativeVMM()
	record, err := v.nativeRecoveryRuntime().journal.read(instance)
	if err != nil {
		return err
	}
	if record.RegionalRuntime == nil || record.RegionalRuntime.Permit != p || record.KernelBootID != m.runtimeHostJournal.Identity().BootID {
		return runtimequotahost.ErrBinding
	}
	if err := m.runtimeHostJournal.CheckRetainedPlacement(*record.RegionalRuntime); err != nil {
		return err
	}
	nc, workloads, err := m.regionalNativeCleanupIdentity(record)
	if err != nil {
		return err
	}
	return m.cleanup(context.WithoutCancel(ctx), record.Lease, nc, workloads)
}

func (m *Manager) regionalNativeCleanupIdentity(record nativeLaunchRecord) (netns.Config, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	nc := nativeLeaseNetwork(record.Lease)
	var workloads []string
	if retained := m.pendingCleanup[record.Lease.Instance]; retained != nil {
		if !sameNativePhysicalLease(retained.lease, record.Lease) {
			return nc, nil, runtimequotahost.ErrBinding
		}
		nc, workloads = retained.net, retained.workloadNames
	}
	if inst := m.live[record.Lease.Instance]; inst != nil {
		if inst.nativeGeneration != record.Generation || !sameNativePhysicalLease(inst.Lease, record.Lease) {
			return nc, nil, runtimequotahost.ErrBinding
		}
		nc, workloads = inst.Net, inst.WorkloadNames
	}
	return nc, append([]string(nil), workloads...), nil
}

// The durable acknowledgements are necessary, but not sufficient. Recheck
// actual kernel tasks and native resources under the original physical lock.
// Neither a successful Destroy nor an empty live map supplies this evidence.
func (m *Manager) verifyRegionalNativeStop(ctx context.Context, instance string, p runtimequota.CapacityPermit) (result error) {
	r := m.nativeVMM().nativeRecoveryRuntime()
	if r.journal == nil || r.resources == nil {
		return runtimequotahost.ErrStorage
	}
	lock, err := r.journal.lock(ctx, instance)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	record, err := r.journal.read(instance)
	if err != nil {
		return err
	}
	if record.RegionalRuntime == nil || record.RegionalRuntime.Permit != p || !record.Revoked || !record.ExitConfirmed || !record.ResourcesRemoved || record.KernelBootID != m.runtimeHostJournal.Identity().BootID {
		return runtimequotahost.ErrBinding
	}
	processes, err := r.retirer.probe.processes(ctx)
	if err != nil {
		return err
	}
	for _, process := range processes {
		if process.Instance == record.Lease.Instance {
			return fmt.Errorf("regional native stop: original instance still has a kernel task: %w", runtimequotahost.ErrBusy)
		}
	}
	if err := r.resources(record.Lease, nativeLeaseNetwork(record.Lease)); err != nil {
		return fmt.Errorf("regional native stop: resources are not absent: %w", err)
	}
	return ctx.Err()
}
