// adr: 20261017201014
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

type rehearsalResumeReadiness interface {
	WaitRehearsalResumeReady(context.Context, Lease, string, int) error
}

// WaitRehearsalResumeReady uses the native boot readiness probe. Rehearsal V1
// admits HTTP/TCP-serving workloads only; no snapshot is captured or restored.
func (v *JailerVMM) WaitRehearsalResumeReady(ctx context.Context, l Lease, path string, deadline int) error {
	return v.waitReady(ctx, l, path, deadline)
}

// RehearseSuspendResume owns a single destruction-joined flight. A retry cannot
// adopt a frozen VM or reconstruct the original recorder from durable rows.
func (m *Manager) RehearseSuspendResume(ctx context.Context, instance, receipt string) (err error) {
	operation, inst, flight, err := m.registerLiveInstanceFlight(ctx, instance)
	if err != nil {
		return err
	}
	defer m.finishInstanceFlight(instance, flight)
	f, ok := m.vmm.(vmFreezer)
	ready, readinessOK := m.vmm.(rehearsalResumeReadiness)
	m.mu.Lock()
	session := m.rehearsalRecorders[instance]
	refusal := freezeRefusal(inst)
	frozen := inst.Frozen
	m.mu.Unlock()
	if !ok || !readinessOK || session == nil || receipt == "" || refusal != "" || frozen || inst.HealthcheckGRPC || inst.ExecutionMode == "worker" || inst.ExecutionMode == "job" || inst.Net.RehearsalRunID == "" {
		return errors.New("original suspend/resume VM capability unavailable")
	}
	store, supported := m.rehearsalNetworkPolicies.(interface {
		FailScenarioSuspendResume(context.Context, string) error
	})
	if !supported {
		return errors.New("suspend/resume receipt storage unavailable")
	}
	defer func() {
		if err != nil {
			session.Fail()
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = store.FailScenarioSuspendResume(cleanup, receipt)
		}
	}()
	if err = m.checkLiveAdmission(operation, inst); err != nil {
		return err
	}
	gate, _, err := m.announceFreeze(inst)
	if err != nil {
		return err
	}
	defer m.settleFreeze(inst, gate)
	checkDispatch := func() error { return nil }
	held := session.HeldSuspendCheckpoint(receipt)
	if held {
		receipts, supported := m.rehearsalNetworkPolicies.(interface {
			ScenarioSuspendResume(context.Context, string) (rehearsalresume.Receipt, error)
		})
		activity, tracked := m.freezeActivity.(interface {
			OnlyDispatch(string, string, int) bool
		})
		if !supported || !tracked {
			return errors.New("running-job forward ownership unavailable")
		}
		r, readErr := receipts.ScenarioSuspendResume(operation, receipt)
		if readErr != nil || r.ID != receipt || r.InstanceID != instance || r.InvocationID == "" {
			return ErrInstanceBusy
		}
		checkDispatch = func() error {
			if !session.HeldSuspendCheckpoint(receipt) || !activity.OnlyDispatch(instance, r.InvocationID, r.InvocationAttempt) {
				return errors.New("original running-job forward or checkpoint was lost")
			}
			return nil
		}
		if err = checkDispatch(); err != nil {
			return err
		}
	} else if m.forwardsInFlight(instance) {
		return ErrInstanceBusy
	}
	if err = m.QuietResume(operation, instance); err != nil {
		return err
	}
	spec, err := session.PrepareSuspend(operation, receipt)
	if err != nil {
		return err
	}
	if _, _, validationErr := rehearsalresume.Normalize(spec, spec.Workloads); validationErr != nil {
		return errors.New("invalid durable suspend/resume plan")
	}
	m.cancelLivenessLoop(instance)
	m.cancelReadinessLoop(instance)
	m.cancelFrameworkReadyLoop(instance)
	if err = checkDispatch(); err != nil {
		return err
	}
	if err = f.PauseVM(operation, inst.Lease); err != nil {
		return fmt.Errorf("rehearsal pause: %w", err)
	}
	pausedAt := time.Now()
	m.mu.Lock()
	inst.Frozen, inst.FrozenSince = true, pausedAt
	m.mu.Unlock()
	if err = session.RecordSuspended(operation, receipt); err != nil {
		return err
	}
	timer := time.NewTimer(time.Duration(spec.PauseMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-operation.Done():
		return operation.Err()
	case <-timer.C:
	}
	started := time.Now()
	if err = checkDispatch(); err != nil {
		return err
	}
	if err = m.checkLiveAdmission(operation, inst); err != nil {
		return err
	}
	// The same network namespace is revalidated before any vCPU resumes.
	if err = m.ensureRehearsalNetwork(operation, inst); err != nil {
		return err
	}
	if err = session.BeginResume(operation, receipt, time.Since(pausedAt).Milliseconds()); err != nil {
		return err
	}
	if err = m.resumeAndProbe(operation, "rehearsal resume", inst, true); err != nil {
		return err
	}
	if err = ready.WaitRehearsalResumeReady(operation, inst.Lease, inst.HealthcheckPath, inst.StartupDeadlineS); err != nil {
		return err
	}
	if err = operation.Err(); err != nil {
		return err
	}
	if err = checkDispatch(); err != nil {
		return err
	}
	return session.RecordResumed(operation, receipt, time.Since(started).Milliseconds())
}

// RehearsalCheckpointHeld is a local owner capability, never reconstructed from
// durable evidence. vmmd must preserve its active forwarding bridge.
func (m *Manager) RehearsalCheckpointHeld(instance, receipt string) bool {
	m.mu.Lock()
	s := m.rehearsalRecorders[instance]
	m.mu.Unlock()
	return s != nil && s.HeldSuspendCheckpoint(receipt)
}
