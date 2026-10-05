package fcvm

// adr: 498
import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

// ResumeRegionalRuntime can replace only a resident's serving authorization.
// Once registered, the new seat stays pinned even after an uncertain native
// error. Retrying cannot restore the old seat or release either physical hold.
func (m *Manager) ResumeRegionalRuntime(ctx context.Context, instance string, p runtimequota.Placement) error {
	if m == nil || m.vmm == nil || m.runtimeHostJournal == nil {
		return runtimequotahost.ErrStorage
	}
	p = p.Clone()
	m.mu.Lock()
	inst := m.live[instance]
	m.mu.Unlock()
	if inst == nil {
		return runtimequotahost.ErrBinding
	}
	admission := admissionForInstance(inst)
	if admission.RegionalRuntime == nil || admission.RegionalRuntime.Permit != p.Permit || p.Seat == nil {
		return runtimequotahost.ErrBinding
	}
	resumer, ok := m.vmm.(ServingResumeVMM)
	if !ok {
		return runtimequotahost.ErrBinding
	}
	admission.RegionalRuntime = &p
	admission.RegionalResumePending = false // Only this guarded resume may retry.
	if err := m.authorizeRuntimeOwner(ctx, admission); err != nil {
		return err
	}
	finish, err := m.runtimeHostJournal.BeginResume(ctx, p)
	if err != nil {
		return err
	}
	defer finish()
	inst.regionalRuntimeMu.Lock()
	inst.regionalRuntime = cloneRuntimePlacement(&p)
	inst.regionalResumePending = true
	spec := ServingResumeSpec{
		AfterPausedRestore: inst.pausedRestorePending,
		HealthcheckPath:    inst.HealthcheckPath, HealthcheckGRPC: inst.HealthcheckGRPC,
		HealthcheckGRPCService: inst.HealthcheckGRPCService, StartupDeadlineS: inst.StartupDeadlineS,
	}
	inst.regionalRuntimeMu.Unlock()
	if err := resumer.ResumeServingVM(ctx, inst.Lease, spec); err != nil {
		return fmt.Errorf("regional resume %s: %w", instance, err)
	}
	// The native backend completed the deferred hook and startup probe. A
	// later authority failure still keeps serving pending, but an exact-seat
	// retry need not repeat a known completed application restore hook.
	inst.regionalRuntimeMu.Lock()
	inst.pausedRestorePending = false
	inst.regionalRuntimeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.authorizeRuntime(ctx, admission); err != nil {
		return err
	}
	m.mu.Lock()
	if m.live[instance] != inst {
		m.mu.Unlock()
		return runtimequotahost.ErrBinding
	}
	inst.Paused = false
	inst.regionalRuntimeMu.Lock()
	inst.regionalResumePending = false
	inst.regionalRuntimeMu.Unlock()
	m.mu.Unlock()
	m.startLivenessLoop(context.WithoutCancel(ctx), instance, inst.Lease.Slot, inst.LivenessProbe)
	m.startReadinessLoop(context.WithoutCancel(ctx), instance, inst.Lease.Slot, inst.ReadinessProbe)
	m.startFrameworkReadyLoop(context.WithoutCancel(ctx), instance)
	return nil
}
