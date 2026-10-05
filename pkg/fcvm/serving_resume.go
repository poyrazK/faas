package fcvm

// adr: 624
import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

// ServingResumeSpec is derived from the retained host resident. Only a restore
// loaded with KeepPaused still owes the post-restore entropy/clock hook.
type ServingResumeSpec struct {
	AfterPausedRestore     bool
	HealthcheckPath        string
	HealthcheckGRPC        bool
	HealthcheckGRPCService string
	StartupDeadlineS       int
}

// ServingResumeVMM acknowledges serving only after native resume, the deferred
// restore hook when needed, and the resident's startup readiness probe. Plain
// ResumeVM remains the pause/snapshot recovery primitive.
type ServingResumeVMM interface {
	ResumeServingVM(context.Context, Lease, ServingResumeSpec) error
}

func (m *Manager) completePausedRestore(ctx context.Context, inst *Instance) error {
	inst.regionalRuntimeMu.Lock()
	if inst.pausedRestoreCompleting {
		inst.regionalRuntimeMu.Unlock()
		return runtimequotahost.ErrBusy
	}
	if !inst.pausedRestorePending {
		inst.regionalRuntimeMu.Unlock()
		return m.vmm.ResumeVM(ctx, inst.Lease)
	}
	inst.pausedRestoreCompleting = true
	inst.regionalRuntimeMu.Unlock()
	defer func() {
		inst.regionalRuntimeMu.Lock()
		inst.pausedRestoreCompleting = false
		inst.regionalRuntimeMu.Unlock()
	}()
	resumer, ok := m.vmm.(ServingResumeVMM)
	if !ok {
		return fmt.Errorf("paused restore: serving completion unavailable")
	}
	spec := ServingResumeSpec{AfterPausedRestore: true, HealthcheckPath: inst.HealthcheckPath,
		HealthcheckGRPC: inst.HealthcheckGRPC, HealthcheckGRPCService: inst.HealthcheckGRPCService,
		StartupDeadlineS: inst.StartupDeadlineS}
	if err := resumer.ResumeServingVM(ctx, inst.Lease, spec); err != nil {
		return err
	}
	inst.regionalRuntimeMu.Lock()
	inst.pausedRestorePending = false
	inst.regionalRuntimeMu.Unlock()
	return nil
}

func (v *JailerVMM) ResumeServingVM(ctx context.Context, l Lease, spec ServingResumeSpec) error {
	if err := v.ResumeVM(ctx, l); err != nil {
		return err
	}
	if spec.AfterPausedRestore {
		if err := v.TriggerResumeHook(ctx, l, time.Now().UnixNano()); err != nil {
			return fmt.Errorf("vmm: deferred restore resume hook: %w", err)
		}
	}
	if err := v.waitReadyWithProbe(ctx, l, spec.HealthcheckPath, spec.HealthcheckGRPC, spec.HealthcheckGRPCService, spec.StartupDeadlineS); err != nil {
		return fmt.Errorf("vmm: readiness after serving resume: %w", err)
	}
	return ctx.Err()
}
