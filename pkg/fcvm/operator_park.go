package fcvm

// adr: 1525. No unknown-instance cleanup and no receipt from mere VM absence.

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

func physicalOperatorGeneration(inst *Instance) string {
	if inst == nil {
		return ""
	}
	if inst.nativeGeneration != "" {
		return inst.nativeGeneration
	}
	return inst.operatorRestartGeneration
}

func (m *Manager) beginOperatorSnapshotFlight(ctx context.Context, instance, generation string) (context.Context, *Instance, *instanceFlight, error) {
	if generation == "" {
		return m.beginLiveInstanceFlight(ctx, instance)
	}
	operationCtx, inst, flight, err := m.registerLiveInstanceGeneration(ctx, instance, generation)
	if err != nil {
		return nil, nil, nil, err
	}
	m.quietResumeBestEffort(operationCtx, instance)
	return operationCtx, inst, flight, nil
}

func (m *Manager) ExecuteOperatorPark(ctx context.Context, t state.OperatorParkTarget) (state.OperatorParkReceipt, error) {
	if t.Generation == "" {
		return state.OperatorParkReceipt{}, fmt.Errorf("operator park: missing physical generation")
	}
	if t.Action == state.OperatorParkStop {
		return m.stopOperatorPark(ctx, t, t.Reason)
	}
	if t.Action != state.OperatorParkCapture {
		return state.OperatorParkReceipt{}, fmt.Errorf("operator park: invalid action")
	}
	if t.FCVersion != m.fcVersion {
		return m.stopOperatorPark(ctx, t, "Firecracker version changed; cold boot required.")
	}
	spec := SnapshotSpec{StorageKey: t.MemoryKey, VMStateStorageKey: t.VMStateKey, BeforeCheckpoint: t.BeforeCheckpoint, operatorGeneration: t.Generation}
	if t.Measured {
		return m.captureAdmittedOperatorPark(ctx, t, spec)
	}
	info, cleaned, err := m.parkWithCleanupResult(ctx, t.InstanceID, spec)
	return operatorParkResult(t, info, cleaned, nil, err)
}

func operatorParkResult(t state.OperatorParkTarget, info SnapshotInfo, cleaned bool, ack *runtimeadmission.SnapshotAcknowledgment, err error) (state.OperatorParkReceipt, error) {
	if !cleaned {
		if err == nil {
			err = fmt.Errorf("operator park lacks confirmed cleanup")
		}
		return state.OperatorParkReceipt{}, err
	}
	if err != nil {
		return state.OperatorParkReceipt{Outcome: string(state.StateStopped), Reason: safetext.Truncate(err.Error(), api.AuditReasonMaxBytes)}, nil
	}
	r := state.OperatorParkReceipt{Outcome: string(state.StateParked), MemBytes: info.MemBytes, VMStateBytes: info.VMStateBytes, StoredBytes: info.StoredBytes, Acknowledgment: ack}
	if err := r.Validate(t); err != nil {
		return state.OperatorParkReceipt{Outcome: string(state.StateStopped), Reason: "Capture evidence was invalid; original VM cleanup was confirmed."}, nil //nolint:nilerr // Confirmed cleanup permits a stopped outcome; invalid capture cannot claim parked.
	}
	return r, nil
}

func (m *Manager) captureAdmittedOperatorPark(ctx context.Context, t state.OperatorParkTarget, spec SnapshotSpec) (state.OperatorParkReceipt, error) {
	if t.Grant == nil {
		return state.OperatorParkReceipt{}, runtimeadmission.ErrInvalid
	}
	g := t.Grant.Clone()
	if g.Mode != "park" || g.Parent.Binding.InstanceID != t.InstanceID || g.MemoryKey != t.MemoryKey || g.VMStateKey != t.VMStateKey || g.BeforeCheckpoint != t.BeforeCheckpoint {
		return state.OperatorParkReceipt{}, runtimeadmission.ErrInvalid
	}
	if err := g.Validate(time.Now()); err != nil {
		return m.stopOperatorPark(ctx, t, "Capture grant expired; cold boot required.")
	}
	if err := m.checkSnapshotGrantIdentity(g); err != nil {
		return state.OperatorParkReceipt{}, err
	}
	flightCtx, cancel := context.WithDeadline(ctx, time.Unix(0, g.ExpiresAtUnixNano))
	defer cancel()
	unlock, err := m.lockAppEgressPolicyForWake(flightCtx, g.Parent.Binding.AppID)
	if err != nil {
		return state.OperatorParkReceipt{}, err
	}
	defer unlock()
	flight := &runtimeAdmissionFlight{ctx: flightCtx, cancel: cancel, done: make(chan struct{})}
	inst, err := m.consumeSnapshotGrant(g, flight)
	if err != nil {
		return state.OperatorParkReceipt{}, err
	}
	defer m.finishRuntimeAdmissionFlight(t.InstanceID, flight)
	spec.admittedGrant = &g
	info, cleaned, captureErr := m.parkWithCleanupResult(flightCtx, t.InstanceID, spec)
	checked, ack, err := m.finishAdmittedSnapshot(flightCtx, g, inst, flight, info, captureErr)
	// This path owns the cleanup result. Never call failedAdmittedSnapshot's
	// ordinary Destroy fallback: a replacement may exist after this flight ends.
	return operatorParkResult(t, checked, cleaned, &ack, err)
}

func (m *Manager) stopOperatorPark(ctx context.Context, t state.OperatorParkTarget, reason string) (state.OperatorParkReceipt, error) {
	m.mu.Lock()
	expected := m.live[t.InstanceID]
	current := physicalOperatorGeneration(expected)
	m.mu.Unlock()
	if current == "" || current != t.Generation {
		return state.OperatorParkReceipt{}, fmt.Errorf("operator park: physical VM generation changed")
	}
	flight, owner, err := m.beginLiveInstanceTeardown(ctx, t.InstanceID, expected)
	if err != nil {
		return state.OperatorParkReceipt{}, err
	}
	if !owner {
		return state.OperatorParkReceipt{}, fmt.Errorf("operator park: another teardown owns this VM")
	}
	var cleanupErr error
	defer func() { m.finishInstanceTeardown(t.InstanceID, flight, 0, false, false, cleanupErr) }()
	if err = m.joinForTeardown(ctx, t.InstanceID); err != nil {
		cleanupErr = err
		return state.OperatorParkReceipt{}, err
	}
	if m.teardownIdentity(t.InstanceID) != expected {
		cleanupErr = fmt.Errorf("operator park: owned VM disappeared before stop")
		return state.OperatorParkReceipt{}, cleanupErr
	}
	m.quietResumeBestEffort(ctx, t.InstanceID)
	m.retainCleanup(expected.Lease, expected.Net, expected.WorkloadNames)
	if t.Signal != 0 {
		var signalErr error
		if signaler, ok := m.vmm.(interface {
			SignalJob(context.Context, Lease, syscall.Signal, time.Duration) (bool, int32, error)
		}); expected.IsJob && ok {
			_, _, signalErr = signaler.SignalJob(ctx, expected.Lease, syscall.Signal(t.Signal), time.Duration(t.GraceSeconds)*time.Second)
		} else {
			_, _, signalErr = m.vmm.SignalAndKill(ctx, expected.Lease, syscall.Signal(t.Signal), time.Duration(t.GraceSeconds)*time.Second)
		}
		if signalErr != nil {
			reason = safetext.Truncate(reason+" Signal phase: "+signalErr.Error(), api.AuditReasonMaxBytes)
		}
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.OperatorParkCleanupTimeout)
	defer cancel()
	cleanupErr = m.cleanup(cleanupCtx, expected.Lease, expected.Net, expected.WorkloadNames)
	if cleanupErr != nil {
		return state.OperatorParkReceipt{}, cleanupErr
	}
	return state.OperatorParkReceipt{Outcome: string(state.StateStopped), Reason: reason}, nil
}
