package fcvm

// adr: 1663. The permission never replaces original physical ownership.

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func (m *Manager) FunctionMigrationSourceAdmissionRequired(instance string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	return inst != nil && m.functionResourcesEnabled(inst) && inst.runtimeAdmissionReceipt.Binding.ProtocolVersion != 0
}

func (m *Manager) PrepareAdmittedFunctionMigrationSource(ctx context.Context, binding FunctionMigrationBinding, spec SnapshotSpec,
	issue func(context.Context, runtimeadmission.Receipt) (runtimeadmission.FunctionMigrationGrant, error),
	check func(context.Context, runtimeadmission.FunctionMigrationGrant) error) (info SnapshotInfo, retErr error) {
	if !m.FunctionMigrationSourceRequired(binding.InstanceID) || issue == nil || check == nil {
		return SnapshotInfo{}, runtimeadmission.ErrUnavailable
	}
	ctx, inst, flight, err := m.registerLiveInstanceFlight(ctx, binding.InstanceID)
	if err != nil {
		return SnapshotInfo{}, err
	}
	defer m.finishInstanceFlight(binding.InstanceID, flight)
	if inst.runtimeAdmissionReceipt.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion || inst.wakeID != binding.SourceWakeID ||
		inst.AppTaskOnly || inst.QualificationOnly || inst.ExecutionOnly || inst.IsJob {
		return SnapshotInfo{}, runtimeadmission.ErrUnavailable
	}
	unlock, err := m.lockAppEgressPolicyForWake(ctx, inst.AppID)
	if err != nil {
		return SnapshotInfo{}, err
	}
	defer unlock()
	source := &functionMigrationSource{binding: binding, inst: inst, managed: true}
	m.mu.Lock()
	_, recoverable := m.vmm.(functionResourceRecoverer)
	if !recoverable || functionExecutionBusy(inst) || inst.functionMigration != nil || m.functionMigrationSources[binding.LeaseToken] != nil {
		m.mu.Unlock()
		return SnapshotInfo{}, ErrInstanceBusy
	}
	if m.functionMigrationSources == nil {
		m.functionMigrationSources = make(map[string]*functionMigrationSource)
	}
	m.functionMigrationSources[binding.LeaseToken], inst.functionMigration = source, source
	m.mu.Unlock()
	effects := false
	defer func() {
		if retErr != nil && !effects {
			m.mu.Lock()
			source.recovered = true // Registration may commit despite a lost acknowledgement.
			m.mu.Unlock()
		}
	}()
	grant, err := issue(ctx, inst.runtimeAdmissionReceipt.Clone())
	if err != nil {
		return SnapshotInfo{}, err
	}
	if err := m.checkFunctionMigrationGrantIdentity(grant, source); err != nil {
		return SnapshotInfo{}, err
	}
	if grant.Purpose != runtimeadmission.FunctionMigrationCapture || grant.MemoryKey != spec.StorageKey || grant.VMStateKey != spec.VMStateStorageKey {
		return SnapshotInfo{}, runtimeadmission.ErrInvalid
	}
	ctx, cancel := context.WithDeadline(ctx, time.Unix(0, grant.ExpiresAtUnixNano))
	defer cancel()
	if err := check(ctx, grant.Clone()); err != nil {
		return SnapshotInfo{}, err
	}
	m.mu.Lock()
	err = m.checkOwnedRuntimeEgressLocked(inst.runtimeAdmissionEgress, grant.Parent.Binding.EgressRevision)
	if err == nil {
		err = m.consumeFunctionMigrationGrantLocked(grant)
	}
	if err == nil {
		owned := grant.Clone()
		source.captureGrant = &owned
	}
	m.mu.Unlock()
	if err != nil {
		return SnapshotInfo{}, err
	}
	spec.functionMigration, spec.functionMigrationGrant = &binding, &grant
	spec.BeforeCheckpoint, spec.ResumeBeforePublish = true, false
	m.quietResumeBestEffort(ctx, binding.InstanceID)
	spec, err = m.configureSnapshotFlight(ctx, binding.InstanceID, inst, spec, snapshotMigration, flight)
	if err != nil {
		return SnapshotInfo{}, err
	}
	m.DeleteLivenessConsecutiveFailures(binding.InstanceID)
	m.cancelLivenessLoop(binding.InstanceID)
	m.cancelReadinessLoop(binding.InstanceID)
	m.cancelFrameworkReadyLoop(binding.InstanceID)
	effects = true
	m.mu.Lock()
	source.failed = true // An uncertain attempt can retire, never replay callbacks.
	m.mu.Unlock()
	info, err = m.vmm.SnapshotKeepAlive(ctx, inst.Lease, spec)
	info, err = checkedNativeSnapshotResult(ctx, spec, info, err)
	if err == nil && (info.MemBytes <= 0 || info.VMStateBytes <= 0) {
		err = runtimeadmission.ErrInvalid
	}
	if err == nil {
		err = check(ctx, grant.Clone())
	}
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("admitted function migration capture: %w", err)
	}
	m.mu.Lock()
	source.failed = false
	m.mu.Unlock()
	m.writeSnapshotBacking(ctx, binding.InstanceID, spec.StorageKey)
	return info, nil
}

func (m *Manager) checkFunctionMigrationGrantIdentity(grant runtimeadmission.FunctionMigrationGrant, source *functionMigrationSource) error {
	if err := grant.Validate(time.Now()); err != nil {
		return err
	}
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		return err
	}
	b := grant.Parent.Binding
	m.mu.Lock()
	inst := source.inst
	parentMatches := grant.Parent.Equal(inst.runtimeAdmissionReceipt) && inst.Lease.Instance == b.InstanceID &&
		inst.AppID == b.AppID && inst.AccountID == b.AccountID && inst.DeploymentID == b.DeploymentID &&
		inst.Net.Netns == grant.Parent.Netns && inst.Lease.HostIP.String() == grant.Parent.HostIP && int32(inst.Lease.UID) == grant.Parent.LeaseUID
	m.mu.Unlock()
	if identity.Validate() != nil || identity.ProtocolVersion < runtimeadmission.ArtifactProtocolVersion || b.NodeID != identity.NodeID ||
		b.Incarnation != identity.Incarnation || grant.FCVersion != m.fcVersion || b.InstanceID != source.binding.InstanceID ||
		b.NodeID != source.binding.SourceNodeID || grant.SourceWakeID != source.binding.SourceWakeID || grant.LeaseToken != source.binding.LeaseToken ||
		!parentMatches {
		return runtimeadmission.ErrStale
	}
	return nil
}

func (m *Manager) RecoverAdmittedFunctionMigrationSource(ctx context.Context, binding FunctionMigrationBinding, grant runtimeadmission.FunctionMigrationGrant,
	check func(context.Context, runtimeadmission.FunctionMigrationGrant) error) (bool, error) {
	if grant.Purpose != runtimeadmission.FunctionMigrationRecover || check == nil {
		return false, runtimeadmission.ErrUnavailable
	}
	m.mu.Lock()
	source := m.functionMigrationSources[binding.LeaseToken]
	if source == nil || source.binding != binding || !source.managed {
		m.mu.Unlock()
		return false, runtimeadmission.ErrStale
	}
	var capture *runtimeadmission.FunctionMigrationGrant
	if source.captureGrant != nil {
		owned := source.captureGrant.Clone()
		capture = &owned
	}
	noEffects := source.recovered && !source.failed
	m.mu.Unlock()
	if err := m.checkFunctionMigrationGrantIdentity(grant, source); err != nil {
		return false, err
	}
	if capture == nil && !noEffects || capture != nil && !grant.SameSource(*capture) {
		return false, runtimeadmission.ErrStale
	}
	ctx, cancel := context.WithDeadline(ctx, time.Unix(0, grant.ExpiresAtUnixNano))
	defer cancel()
	return m.recoverFunctionMigrationSource(ctx, binding, &grant, func(ctx context.Context) error { return check(ctx, grant.Clone()) })
}

// Called with Manager.mu, using the same bounded replay window as native boot.
func (m *Manager) consumeFunctionMigrationGrantLocked(grant runtimeadmission.FunctionMigrationGrant) error {
	now := time.Now()
	if err := grant.Validate(now); err != nil {
		return err
	}
	for token, expiry := range m.runtimeAdmissionTokens {
		if !now.Before(expiry) {
			delete(m.runtimeAdmissionTokens, token)
		}
	}
	if _, used := m.runtimeAdmissionTokens[grant.Token]; used {
		return runtimeadmission.ErrReplay
	}
	if len(m.runtimeAdmissionTokens) >= api.ApplicationStandardRuntimeAdmissionReplayLimit {
		return runtimeadmission.ErrCapacity
	}
	if m.runtimeAdmissionTokens == nil {
		m.runtimeAdmissionTokens = make(map[string]time.Time)
	}
	m.runtimeAdmissionTokens[grant.Token] = time.Unix(0, grant.ExpiresAtUnixNano)
	return nil
}

func (m *Manager) checkFunctionMigrationSnapshotLocked(instance string, spec SnapshotSpec, parent runtimeadmission.Receipt) error {
	grant := spec.functionMigrationGrant
	inst, flight := m.live[instance], m.instanceFlights[instance]
	if inst == nil || inst.functionMigration == nil || flight == nil || flight.cancelled || flight.recoveryCtx.Err() != nil ||
		!inst.functionMigration.managed || inst.functionMigration.inst != inst || inst.functionMigration.captureGrant == nil ||
		grant.Validate(time.Now()) != nil || !grant.Equal(*inst.functionMigration.captureGrant) || !grant.Parent.Equal(parent) ||
		grant.Purpose != runtimeadmission.FunctionMigrationCapture || grant.MemoryKey != spec.StorageKey || grant.VMStateKey != spec.VMStateStorageKey ||
		grant.FCVersion != m.fcVersion || !spec.BeforeCheckpoint || spec.ResumeBeforePublish || spec.functionMigration == nil ||
		*spec.functionMigration != inst.functionMigration.binding {
		return runtimeadmission.ErrStale
	}
	return nil
}
