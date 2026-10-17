package fcvm

// adr: 1574. A continuing VM must rebuild resources before admission reopens.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// Recovery uses one application callback attempt. A lost ACK cannot replay a
// custom after_restore callback; uncertainty retires the original physical VM.
type functionResourceRecoverer interface {
	RecoverFunctionResources(context.Context, Lease) error
}

// ErrFunctionWarmCapture marks failures after checkpoint admission. Nested
// busy/admission errors must not describe a healthy pre-capture refusal.
var ErrFunctionWarmCapture = errors.New("function warm capture did not complete")

var errWarmSnapshotAdmissionBusy = errors.New("warm snapshot admission refused before capture")

func (m *Manager) functionResourcesEnabled(inst *Instance) bool {
	return m.functionActivity != nil && functionlifetime.NativeNode(inst.Runtime)
}

func (m *Manager) warmFunctionSnapshot(ctx context.Context, inst *Instance, spec SnapshotSpec, flight *instanceFlight) (SnapshotInfo, error, bool) {
	// Keep the original guest paused through publication. The same instance
	// flight blocks ordinary Begin through the final all-worker recovery ACK.
	spec.ResumeBeforePublish = false
	instance := inst.Lease.Instance
	m.DeleteLivenessConsecutiveFailures(instance)
	m.cancelLivenessLoop(instance)
	m.cancelReadinessLoop(instance)
	m.cancelFrameworkReadyLoop(instance)
	info, captureErr := m.vmm.SnapshotKeepAlive(ctx, inst.Lease, spec)
	info, captureErr = checkedNativeSnapshotResult(ctx, spec, info, captureErr)
	// Even a cancelled RPC or rejected capture may have drained some pools.
	// Destroy cancels this registered recovery context and joins the flight.
	recoveryCtx, cancel := context.WithTimeout(flight.recoveryCtx, resumeHookDialDeadline)
	defer cancel()
	if err := m.recoverWarmFunction(recoveryCtx, inst, flight); err != nil { //nolint:contextcheck // The destruction-joined recovery context deliberately outlives the capture RPC.
		return SnapshotInfo{}, fmt.Errorf("warm_snapshot %s: %w", instance, errors.Join(ErrFunctionWarmCapture, captureErr, fmt.Errorf("resource recovery: %w", err))), true
	}
	if captureErr != nil {
		return SnapshotInfo{}, fmt.Errorf("warm_snapshot %s: capture: %w", instance, errors.Join(ErrFunctionWarmCapture, captureErr)), false
	}
	if err := ctx.Err(); err != nil {
		return SnapshotInfo{}, errors.Join(ErrFunctionWarmCapture, err), false
	}
	return info, nil, false
}

func (m *Manager) recoverWarmFunction(ctx context.Context, inst *Instance, flight *instanceFlight) error {
	if err := m.functionPauseBarrier(inst.Lease.Instance); err != nil {
		return err
	}
	if err := m.checkLiveAdmission(ctx, inst); err != nil {
		return err
	}
	if err := m.vmm.ResumeVM(ctx, inst.Lease); err != nil {
		return err
	}
	m.clearFrozen(inst)
	if err := m.vmm.(functionResourceRecoverer).RecoverFunctionResources(ctx, inst.Lease); err != nil {
		return err
	}
	m.mu.Lock()
	current := m.live[inst.Lease.Instance] == inst && m.instanceFlights[inst.Lease.Instance] == flight && !flight.cancelled && !functionExecutionBusy(inst)
	m.mu.Unlock()
	if !current {
		return runtimeadmission.ErrStale
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.startLivenessLoop(context.WithoutCancel(ctx), inst.Lease.Instance, inst.Lease.Slot, inst.LivenessProbe)
	m.startReadinessLoop(context.WithoutCancel(ctx), inst.Lease.Instance, inst.Lease.Slot, inst.ReadinessProbe)
	m.startFrameworkReadyLoop(context.WithoutCancel(ctx), inst.Lease.Instance)
	return nil
}

// Seal admission before releasing the flight, including if teardown later
// fails. Only confirmed physical retirement releases execution/RAM ownership.
func (m *Manager) failFunctionResources(inst *Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst.functionExecutions == nil {
		inst.functionExecutions = &functionExecutionState{active: make(map[uint64]time.Time)}
	}
	inst.functionExecutions.failed = true
}

func (v *JailerVMM) RecoverFunctionResources(ctx context.Context, lease Lease) error {
	ctx, cancel := context.WithTimeout(ctx, resumeHookDialDeadline)
	defer cancel()
	return errors.Join(v.triggerResumeHookOnce(ctx, lease, time.Now().UnixNano(), nil), ctx.Err())
}
