package fcvm

// adr: 20261018030615. A destination stays closed until schedd's ownership commit.

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/wire"
)

type functionMigrationDestination struct {
	grant              runtimeadmission.FunctionMigrationDestinationGrant
	cancel             context.CancelFunc
	done               chan struct{}
	inst               *Instance
	receipt            runtimeadmission.Receipt
	err                error
	published, retired bool
	retiring           chan struct{}
}

// Dedicated qualification capability; ordinary measured snapshot restore stays off.
func (m *Manager) FunctionMigrationDestinationVersion() uint32 {
	_, ok := m.vmm.(consumedSnapshotVMM)
	if functionlifetime.NativeQualificationBuild && ok && m.runtimeAdmissionProtocol() >= runtimeadmission.ArtifactProtocolVersion && m.functionActivity != nil {
		return 1
	}
	return 0
}
func (m *Manager) functionDestinationForWake(req WakeRequest) *functionMigrationDestination {
	if req.functionMigrationDestination == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.functionMigrationDestinations[req.Instance]
}

func (m *Manager) AdoptFunctionMigrationDestination(ctx context.Context, request AdmittedWakeRequest, check func(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error) (*Instance, runtimeadmission.Receipt, error) {
	if request.Migration == nil || check == nil || m.FunctionMigrationDestinationVersion() != 1 || !functionlifetime.NativeNode(request.Request.Runtime) {
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrUnavailable
	}
	g := request.Migration.Clone()
	if g.Validate(time.Now()) != nil || request.Binding != g.Binding || request.NativeInputHash != g.Binding.PayloadHash {
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrInvalid
	}
	if err := check(ctx, g.Clone()); err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	m.mu.Lock()
	m.purgeFunctionDestinationHistoryLocked()
	old := m.functionMigrationDestinations[g.Binding.InstanceID]
	if old != nil {
		if !old.grant.Equal(g) {
			m.mu.Unlock()
			return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrReplay
		}
		done := old.done
		m.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, runtimeadmission.Receipt{}, ctx.Err()
		}
		if err := check(ctx, g.Clone()); err != nil {
			return nil, runtimeadmission.Receipt{}, err
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if old.retired || old.err != nil || m.live[g.Binding.InstanceID] != old.inst {
			return nil, runtimeadmission.Receipt{}, errors.Join(runtimeadmission.ErrStale, old.err)
		}
		return old.inst, old.receipt.Clone(), nil
	}
	if len(m.functionMigrationDestinations) >= api.ApplicationStandardRuntimeAdmissionReplayLimit {
		m.mu.Unlock()
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrCapacity
	}
	if m.live[g.Binding.InstanceID] != nil || m.instanceFlights[g.Binding.InstanceID] != nil || m.pendingCleanup[g.Binding.InstanceID] != nil {
		m.mu.Unlock()
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrReplay
	}
	flightCtx, cancel := context.WithDeadline(ctx, time.Unix(0, g.Binding.ExpiresAtUnixNano))
	d := &functionMigrationDestination{grant: g.Clone(), cancel: cancel, done: make(chan struct{})}
	if m.functionMigrationDestinations == nil {
		m.functionMigrationDestinations = make(map[string]*functionMigrationDestination)
	}
	m.functionMigrationDestinations[g.Binding.InstanceID] = d
	m.mu.Unlock()
	defer cancel()
	fields, _ := wire.FromContext(flightCtx)
	fields.WakeID = g.WakeID
	flightCtx = wire.WithContext(flightCtx, fields)
	request.Migration = &g
	request.migrationOwner = d
	inst, receipt, err := m.WakeAdmitted(flightCtx, request, nil)
	if err == nil {
		err = g.CheckReceipt(receipt, time.Now())
	}
	if err == nil {
		err = check(flightCtx, g.Clone())
	}
	m.mu.Lock()
	d.inst, d.receipt, d.err = inst, receipt.Clone(), err
	m.mu.Unlock()
	if err != nil {
		if inst != nil {
			m.failFunctionResources(inst)
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		cleanupErr := m.Destroy(cleanupCtx, g.Binding.InstanceID)
		cleanupCancel()
		m.mu.Lock()
		d.retired = cleanupErr == nil
		d.err = errors.Join(err, cleanupErr)
		m.mu.Unlock()
	}
	m.mu.Lock()
	close(d.done)
	retErr := d.err
	m.mu.Unlock()
	if retErr != nil {
		return nil, runtimeadmission.Receipt{}, retErr
	}
	return inst, receipt.Clone(), nil
}

func (m *Manager) ActivateFunctionMigrationDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant, check func(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error) (*Instance, runtimeadmission.Receipt, error) {
	if m.FunctionMigrationDestinationVersion() != 1 || check == nil {
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrUnavailable
	}
	unlock, err := m.lockAppEgressPolicyForWake(ctx, g.Binding.AppID)
	if err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	defer unlock()
	if err := check(ctx, g.Clone()); err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	if err := m.checkAppAdmission(ctx, g.Binding.AppID); err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	flightCtx, flight := newInstanceFlight(ctx)
	m.mu.Lock()
	d := m.functionMigrationDestinations[g.Binding.InstanceID]
	if d == nil || !d.grant.Equal(g) || d.retired || d.err != nil || d.inst == nil || m.live[g.Binding.InstanceID] != d.inst || d.inst.wakeID != g.WakeID || m.instanceStops[g.Binding.InstanceID] != nil || m.instanceFlights[g.Binding.InstanceID] != nil || m.pendingCleanup[g.Binding.InstanceID] != nil || !d.published && functionExecutionBusy(d.inst) {
		m.mu.Unlock()
		flight.cancel()
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrStale
	}
	if err := m.checkOwnedRuntimeEgressLocked(d.inst.runtimeAdmissionEgress, g.Binding.EgressRevision); err != nil {
		m.mu.Unlock()
		flight.cancel()
		return nil, runtimeadmission.Receipt{}, err
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		flight.cancel()
		return nil, runtimeadmission.Receipt{}, err
	}
	if d.published {
		m.mu.Unlock()
		flight.cancel()
		return d.inst, d.receipt.Clone(), nil
	}
	// Restore authority bounds loading and publication. Current durable serving
	// authority opens this already-ready owner without another callback.
	d.published = true
	flight.functionExecutionAdmission = true
	if m.instanceFlights == nil {
		m.instanceFlights = make(map[string]*instanceFlight)
	}
	m.instanceFlights[g.Binding.InstanceID] = flight
	m.mu.Unlock()
	defer m.finishInstanceFlight(g.Binding.InstanceID, flight)
	inst := d.inst
	m.startLivenessLoop(flightCtx, g.Binding.InstanceID, inst.Lease.Slot, inst.LivenessProbe)
	m.startReadinessLoop(flightCtx, g.Binding.InstanceID, inst.Lease.Slot, inst.ReadinessProbe)
	m.startFrameworkReadyLoop(flightCtx, g.Binding.InstanceID)
	if err := flightCtx.Err(); err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	return inst, d.receipt.Clone(), nil
}

func (m *Manager) RetireFunctionMigrationDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant, check func(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error) error {
	if m.FunctionMigrationDestinationVersion() != 1 || check == nil {
		return runtimeadmission.ErrUnavailable
	}
	if err := check(ctx, g.Clone()); err != nil {
		return err
	}
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil || identity.NodeID != g.Binding.NodeID || identity.Incarnation != g.Binding.Incarnation {
		return runtimeadmission.ErrStale
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.purgeFunctionDestinationHistoryLocked()
	d := m.functionMigrationDestinations[g.Binding.InstanceID]
	if d == nil {
		id := g.Binding.InstanceID
		_, quarantined := m.restartQuarantine[id]
		_, waking := m.waking[id]
		if quarantined || waking || m.live[id] != nil || m.instanceFlights[id] != nil || m.instanceStops[id] != nil || m.pendingCleanup[id] != nil || m.runtimeAdmissionFlights[id] != nil || m.functionStarting[id] != nil {
			m.mu.Unlock()
			return runtimeadmission.ErrStale
		}
		if len(m.functionMigrationDestinations) >= api.ApplicationStandardRuntimeAdmissionReplayLimit {
			m.mu.Unlock()
			return runtimeadmission.ErrCapacity
		}
		// Same-incarnation absence is a known no-effects result. Reserve a
		// tombstone under the adoption mutex before confirming it, so an RPC
		// that passed its permission read earlier cannot load after rollback.
		d = &functionMigrationDestination{grant: g.Clone(), cancel: func() {}, done: make(chan struct{}), retired: true}
		close(d.done)
		if m.functionMigrationDestinations == nil {
			m.functionMigrationDestinations = make(map[string]*functionMigrationDestination)
		}
		m.functionMigrationDestinations[id] = d
		m.mu.Unlock()
		return nil
	}
	if !d.grant.Equal(g) || d.published {
		m.mu.Unlock()
		return runtimeadmission.ErrStale
	}
	d.cancel()
	done := d.done
	m.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	for {
		m.mu.Lock()
		if d.retired {
			m.mu.Unlock()
			return nil
		}
		if wait := d.retiring; wait != nil {
			m.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if d.inst != nil && m.live[g.Binding.InstanceID] != nil && m.live[g.Binding.InstanceID] != d.inst {
			m.mu.Unlock()
			return runtimeadmission.ErrStale
		}
		d.retiring = make(chan struct{})
		wait := d.retiring
		m.mu.Unlock()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		err := m.Destroy(cleanupCtx, g.Binding.InstanceID)
		cancel()
		m.mu.Lock()
		d.retired = err == nil
		close(wait)
		d.retiring = nil
		m.mu.Unlock()
		return err
	}
}

func checkFunctionOrSnapshotRestoreInputs(req SnapshotRestoreInputs, now time.Time) error {
	if req.FunctionMigration == nil {
		return req.Capture.CheckRestoreInputs(req.Binding, req.Sources, req.Snapshot.StorageKey, req.Snapshot.VMStateStorageKey, int64(req.Runtime.MemSizeMiB)<<20, now)
	}
	if !functionlifetime.NativeQualificationBuild {
		return runtimeadmission.ErrUnavailable
	}
	e := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: req.Binding.SnapshotCaptureToken, FCVersion: req.Snapshot.FCVersion, Capture: req.Capture}
	return req.FunctionMigration.CheckRestore(req.Binding, req.Sources, e, req.Snapshot.StorageKey, req.Snapshot.VMStateStorageKey, req.Snapshot.FCVersion, int64(req.Runtime.MemSizeMiB)<<20, now)
}

// Called with Manager.mu. Live/uncertain physical owners retain their entries.
func (m *Manager) purgeFunctionDestinationHistoryLocked() {
	now := time.Now().UnixNano()
	for id, prior := range m.functionMigrationDestinations {
		if (prior.retired || prior.published && m.live[id] == nil && m.pendingCleanup[id] == nil && m.instanceFlights[id] == nil && m.instanceStops[id] == nil) && now >= prior.grant.Binding.ExpiresAtUnixNano {
			delete(m.functionMigrationDestinations, id)
		}
	}
}
