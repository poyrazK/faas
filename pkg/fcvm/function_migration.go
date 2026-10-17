package fcvm

// adr: 1545. Source recovery is bound to the original physical incarnation.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type FunctionMigrationBinding struct {
	InstanceID, LeaseToken, SourceNodeID, SourceWakeID string
}

const FunctionMigrationSourceProtocolVersion uint32 = 1

// All fields are guarded by Manager.mu. Completed results survive retries
// until the durable lease and temporary objects have been cleaned up.
type functionMigrationSource struct {
	binding                    FunctionMigrationBinding
	inst                       *Instance
	recovered, retired, failed bool
	managed                    bool
	captureGrant               *runtimeadmission.FunctionMigrationGrant
}

func functionMigrationActive(inst *Instance) bool {
	return inst != nil && (inst.functionMigration != nil && !inst.functionMigration.recovered || inst.functionDestination != nil && !inst.functionDestination.published)
}

func (m *Manager) FunctionMigrationSourceRequired(instance string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	return inst != nil && m.functionResourcesEnabled(inst)
}

func (m *Manager) PrepareFunctionMigrationSource(ctx context.Context, binding FunctionMigrationBinding, spec SnapshotSpec, register func(context.Context) error) (info SnapshotInfo, retErr error) {
	if !m.FunctionMigrationSourceRequired(binding.InstanceID) || binding.LeaseToken == "" || binding.SourceNodeID == "" || binding.SourceWakeID == "" {
		return SnapshotInfo{}, runtimeadmission.ErrUnavailable
	}
	spec.functionMigration = &binding
	spec.BeforeCheckpoint, spec.ResumeBeforePublish = true, false
	ctx, inst, spec, flight, err := m.beginSnapshotFlight(ctx, binding.InstanceID, spec, snapshotMigration)
	if err != nil {
		return SnapshotInfo{}, err
	}
	defer m.finishInstanceFlight(binding.InstanceID, flight)
	// Fresh measured migration/cancel/destination grants are a separate gate.
	if inst.runtimeAdmissionReceipt.Binding.ProtocolVersion != 0 || inst.wakeID != binding.SourceWakeID || inst.AppTaskOnly || inst.QualificationOnly || inst.ExecutionOnly || inst.IsJob {
		return SnapshotInfo{}, runtimeadmission.ErrUnavailable
	}
	source := &functionMigrationSource{binding: binding, inst: inst}
	m.mu.Lock()
	if inst.functionMigration != nil || m.functionMigrationSources[binding.LeaseToken] != nil {
		m.mu.Unlock()
		return SnapshotInfo{}, ErrInstanceBusy
	}
	if m.functionMigrationSources == nil {
		m.functionMigrationSources = make(map[string]*functionMigrationSource)
	}
	m.functionMigrationSources[binding.LeaseToken], inst.functionMigration = source, source
	m.mu.Unlock()
	// Register the durable identity while this flight pins the original VM,
	// before a checkpoint can drain any application resource.
	if register == nil {
		err = runtimeadmission.ErrUnavailable
	} else {
		err = register(ctx)
	}
	if err != nil {
		m.mu.Lock()
		source.recovered = true
		m.mu.Unlock()
		// Registration can commit despite a lost DB acknowledgement. Retain
		// this known no-effects result for a later fenced lease recovery.
		return SnapshotInfo{}, err
	}
	m.DeleteLivenessConsecutiveFailures(binding.InstanceID)
	m.cancelLivenessLoop(binding.InstanceID)
	m.cancelReadinessLoop(binding.InstanceID)
	m.cancelFrameworkReadyLoop(binding.InstanceID)
	info, err = m.vmm.SnapshotKeepAlive(ctx, inst.Lease, spec)
	info, err = checkedNativeSnapshotResult(ctx, spec, info, err)
	if err == nil && (info.MemBytes <= 0 || info.VMStateBytes <= 0) {
		err = runtimeadmission.ErrInvalid
	}
	if err == nil {
		m.writeSnapshotBacking(ctx, binding.InstanceID, spec.StorageKey)
		return info, nil
	}
	// The lease stays durable. schedd will fence rollback even if the caller
	// never received a token. No unacknowledged request may reopen this source.
	m.mu.Lock()
	source.failed = true
	m.mu.Unlock()
	return SnapshotInfo{}, fmt.Errorf("prepare function migration: %w", err)
}

// RecoverFunctionMigrationSource invokes authorize before and after the one
// resource recovery attempt. The callback must read schedd's current durable
// rollback fence, without a replica/cache. It never writes instance state.
func (m *Manager) RecoverFunctionMigrationSource(ctx context.Context, binding FunctionMigrationBinding, authorize func(context.Context) error) (retired bool, retErr error) {
	return m.recoverFunctionMigrationSource(ctx, binding, nil, authorize)
}

func (m *Manager) recoverFunctionMigrationSource(ctx context.Context, binding FunctionMigrationBinding, permission *runtimeadmission.FunctionMigrationGrant, authorize func(context.Context) error) (retired bool, retErr error) {
	m.mu.Lock()
	source := m.functionMigrationSources[binding.LeaseToken]
	if source == nil || source.binding != binding {
		m.mu.Unlock()
		return false, runtimeadmission.ErrStale
	}
	if source.managed != (permission != nil) {
		m.mu.Unlock()
		return false, runtimeadmission.ErrUnavailable
	}
	if source.retired {
		m.mu.Unlock()
		return true, nil
	}
	if source.recovered {
		healthy := m.live[binding.InstanceID] == source.inst && m.instanceStops[binding.InstanceID] == nil && m.pendingCleanup[binding.InstanceID] == nil &&
			(source.inst.functionExecutions == nil || !source.inst.functionExecutions.failed)
		m.mu.Unlock()
		if !healthy {
			return false, runtimeadmission.ErrStale
		}
		if permission != nil {
			if err := authorize(ctx); err != nil {
				return false, err
			}
		}
		if err := m.checkLiveAdmission(ctx, source.inst); err != nil {
			return false, err
		}
		if permission != nil {
			if err := authorize(ctx); err != nil {
				return false, err
			}
		}
		m.mu.Lock()
		retired = source.retired
		healthy = m.live[binding.InstanceID] == source.inst && m.instanceStops[binding.InstanceID] == nil && m.pendingCleanup[binding.InstanceID] == nil &&
			(source.inst.functionExecutions == nil || !source.inst.functionExecutions.failed)
		m.mu.Unlock()
		if retired {
			return true, nil
		}
		if !healthy {
			return false, runtimeadmission.ErrStale
		}
		return false, nil
	}
	failed, inFlight := source.failed, m.instanceFlights[binding.InstanceID] != nil
	m.mu.Unlock()
	if authorize == nil {
		return false, runtimeadmission.ErrUnavailable
	}
	if failed {
		if inFlight {
			return false, ErrInstanceBusy
		}
		// An earlier attempt has already sealed admission. Retry physical
		// cleanup even when pendingCleanup prevents ordinary registration.
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resumeHookDialDeadline)
		defer cancel()
		if permission != nil {
			var cancelPermission context.CancelFunc
			recoveryCtx, cancelPermission = context.WithDeadline(recoveryCtx, time.Unix(0, permission.ExpiresAtUnixNano))
			defer cancelPermission()
		}
		if err := authorize(recoveryCtx); err != nil {
			return false, err
		}
		m.failFunctionResources(source.inst)
		err := m.retireFunctionMigrationSource(recoveryCtx, source)
		m.mu.Lock()
		retired = source.retired
		m.mu.Unlock()
		return retired, err
	}
	ctx, inst, flight, err := m.registerFunctionMigrationFlight(ctx, source)
	if err != nil {
		return false, err
	}
	retire := false
	defer func() {
		if retire {
			m.failFunctionResources(inst)
		}
		m.finishInstanceFlight(binding.InstanceID, flight)
		if retire {
			retErr = errors.Join(retErr, m.retireFunctionMigrationSource(context.WithoutCancel(ctx), source))
			m.mu.Lock()
			retired = source.retired
			m.mu.Unlock()
			// Retirement is an explicit successful rollback outcome. It never
			// claims pool recovery and is carried separately to schedd.
			if retired {
				retErr = nil
			}
		}
	}()
	recoveryCtx, cancel := context.WithTimeout(flight.recoveryCtx, resumeHookDialDeadline)
	defer cancel()
	if permission != nil {
		var cancelPermission context.CancelFunc
		recoveryCtx, cancelPermission = context.WithDeadline(recoveryCtx, time.Unix(0, permission.ExpiresAtUnixNano))
		defer cancelPermission()
		unlock, err := m.lockAppEgressPolicyForWake(recoveryCtx, inst.AppID) //nolint:contextcheck // Destroy owns cancellation of this recovery context.
		if err != nil {
			return false, err
		}
		defer unlock()
	}
	if err := authorize(recoveryCtx); err != nil { //nolint:contextcheck // Recovery survives RPC cancellation and is cancelled/joined by Destroy.
		return false, err
	}
	m.mu.Lock()
	failed = source.failed
	m.mu.Unlock()
	if failed {
		// A failed capture may have left checkpoint cleanup unconfirmed. Do
		// not turn a retry into a second application callback attempt.
		retire = true
		return false, errors.New("function migration checkpoint did not complete")
	}
	if permission != nil {
		m.mu.Lock()
		err := m.checkOwnedRuntimeEgressLocked(inst.runtimeAdmissionEgress, permission.Parent.Binding.EgressRevision)
		if err == nil {
			err = m.consumeFunctionMigrationGrantLocked(*permission)
		}
		m.mu.Unlock()
		if err != nil {
			return false, err
		}
	}
	m.mu.Lock()
	source.failed = true // A retry can retire, but can never replay this attempt.
	m.mu.Unlock()
	retire = true
	if err := m.recoverWarmFunction(recoveryCtx, inst, flight); err != nil { //nolint:contextcheck // Recovery survives RPC cancellation and is cancelled/joined by Destroy.
		return false, err
	}
	if err := authorize(recoveryCtx); err != nil { //nolint:contextcheck // Recheck the fence under the same destruction-joined recovery context.
		return false, err
	}
	m.mu.Lock()
	if m.live[binding.InstanceID] != inst || flight.cancelled || recoveryCtx.Err() != nil {
		m.mu.Unlock()
		return false, runtimeadmission.ErrStale
	}
	source.recovered = true
	m.mu.Unlock()
	retire = false
	return false, nil
}

func (m *Manager) registerFunctionMigrationFlight(ctx context.Context, source *functionMigrationSource) (context.Context, *Instance, *instanceFlight, error) {
	return m.registerLiveInstanceFlightForMigration(ctx, source.binding.InstanceID, source)
}

// RetireFunctionMigrationSource never targets a replacement with the same ID.
func (m *Manager) RetireFunctionMigrationSource(ctx context.Context, binding FunctionMigrationBinding) error {
	m.mu.Lock()
	source := m.functionMigrationSources[binding.LeaseToken]
	if source == nil || source.binding != binding {
		m.mu.Unlock()
		return runtimeadmission.ErrStale
	}
	m.mu.Unlock()
	return m.retireFunctionMigrationSource(ctx, source)
}

func (m *Manager) retireFunctionMigrationSource(ctx context.Context, source *functionMigrationSource) (retErr error) {
	m.mu.Lock()
	done := source.retired
	m.mu.Unlock()
	if done {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resumeHookDialDeadline)
	defer cancel()
	id := source.binding.InstanceID
	flight, owner, err := m.beginLiveInstanceTeardown(cleanupCtx, id, source.inst)
	if err != nil {
		return err
	}
	if !owner {
		return flight.err
	}
	code := 0
	defer func() { m.finishInstanceTeardown(id, flight, code, false, false, retErr) }()
	code, retErr = m.destroyWithReservation(cleanupCtx, cleanupCtx, id, "")
	return retErr
}

func (m *Manager) ForgetFunctionMigrationSource(binding FunctionMigrationBinding) {
	m.mu.Lock()
	defer m.mu.Unlock()
	source := m.functionMigrationSources[binding.LeaseToken]
	if source == nil || source.binding != binding || !source.retired && !source.recovered {
		return
	}
	if source.inst.functionMigration == source {
		source.inst.functionMigration = nil
	}
	delete(m.functionMigrationSources, binding.LeaseToken)
}

// Called only after physical cleanup and allocator release are confirmed.
func (m *Manager) markFunctionMigrationRetiredLocked(lease Lease) {
	for _, source := range m.functionMigrationSources {
		if source.inst.Lease == lease {
			source.retired = true
		}
	}
}
