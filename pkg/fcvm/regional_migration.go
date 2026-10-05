package fcvm

// adr: 628
import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

func sameMigrationPlacement(a, b *runtimequota.Placement) bool {
	return a != nil && b != nil && a.Permit == b.Permit && a.Seat != nil && b.Seat != nil && *a.Seat == *b.Seat
}

// Called inside SnapshotKeepAlive's instance flight. The seat is fenced before
// capture, and uncertain capture never resumes implicitly or drops its binding.
func (m *Manager) snapshotRegionalMigration(ctx context.Context, inst *Instance, spec SnapshotSpec) (SnapshotInfo, error) {
	a := admissionForInstance(inst)
	if !sameMigrationPlacement(a.RegionalRuntime, spec.RegionalMigrationSource) {
		return SnapshotInfo{}, runtimequotahost.ErrBinding
	}
	if err := m.authorizeRuntimeOwner(ctx, a); err != nil {
		return SnapshotInfo{}, err
	}
	if m.runtimeHostJournal == nil {
		return SnapshotInfo{}, runtimequotahost.ErrStorage
	}
	finish, err := m.runtimeHostJournal.BeginMigrationCapture(ctx, inst.Lease.Instance, spec.MigrationLeaseToken, *a.RegionalRuntime, spec.StorageKey, spec.VMStateStorageKey)
	if err != nil {
		return SnapshotInfo{}, err
	}
	defer finish()
	inst.regionalRuntimeMu.Lock()
	inst.regionalResumePending = true
	inst.regionalRuntimeMu.Unlock()
	m.DeleteLivenessConsecutiveFailures(inst.Lease.Instance)
	m.cancelLivenessLoop(inst.Lease.Instance)
	m.cancelReadinessLoop(inst.Lease.Instance)
	m.cancelFrameworkReadyLoop(inst.Lease.Instance)
	spec.ResumeBeforePublish, spec.BeforeResume = false, nil
	info, err := m.vmm.SnapshotKeepAlive(ctx, inst.Lease, spec)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("regional migration capture: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return SnapshotInfo{}, err
	}
	m.mu.Lock()
	inst.Paused = true
	m.mu.Unlock()
	return info, nil
}

// RegionalMigrationSource retains the original runtime key, full physical
// permit, serving seat and lease even when the instances row has changed.
func (m *Manager) RegionalMigrationSource(_ context.Context, instance, token string) (runtimequotahost.MigrationRecord, error) {
	if m == nil {
		return runtimequotahost.MigrationRecord{}, runtimequotahost.ErrStorage
	}
	if m.runtimeHostJournal == nil {
		return runtimequotahost.MigrationRecord{}, runtimequotahost.ErrMigrationNotFound
	}
	return m.runtimeHostJournal.MigrationSource(instance, token)
}

// ResumeRegionalMigration is explicit and lease-bound. A known completed reply
// can be replayed after restart without treating an empty live map as proof.
// Current authority and fences still apply; unfinished native recovery stays
// quarantined and cannot be resumed through this path.
func (m *Manager) ResumeRegionalMigration(ctx context.Context, instance, token string, p runtimequota.Placement) error {
	r, err := m.RegionalMigrationSource(ctx, instance, token)
	if err != nil {
		return err
	}
	if r.Source.Permit != p.Permit || p.Seat == nil || p.Seat.ID == r.Source.Seat.ID {
		return runtimequotahost.ErrBinding
	}
	if !r.Completed {
		return m.resumeRegionalRuntime(ctx, instance, p, token)
	}
	if !sameMigrationPlacement(r.Resume, &p) {
		return runtimequotahost.ErrBinding
	}
	a := RuntimeAdmission{Instance: instance, AppID: p.Permit.Grant.Binding.RegionalAppID,
		AccountID: p.Permit.Grant.Binding.RegionalAccountID, DeploymentID: p.Permit.DeploymentID,
		Plan: p.Permit.Grant.Policy.Plan, RegionalRuntime: cloneRuntimePlacement(&p)}
	finish, err := m.beginRuntimeOperation(ctx, a)
	if err != nil {
		return err
	}
	defer finish()
	return ctx.Err()
}
