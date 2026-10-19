package fcvm

// adr: 435. Fresh capture authority is consumed against owned native residency.

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func (m *Manager) CaptureAdmitted(ctx context.Context, grant runtimeadmission.SnapshotGrant) (SnapshotInfo, runtimeadmission.SnapshotAcknowledgment, error) {
	g := grant.Clone()
	if err := g.Validate(time.Now()); err != nil {
		return SnapshotInfo{}, runtimeadmission.SnapshotAcknowledgment{}, err
	}
	// Managed migration also needs a fresh cancel/resume and destination grant.
	if g.Mode != "warm" && g.Mode != "park" {
		return SnapshotInfo{}, runtimeadmission.SnapshotAcknowledgment{}, runtimeadmission.ErrUnavailable
	}
	if err := m.checkSnapshotGrantIdentity(g); err != nil {
		return SnapshotInfo{}, runtimeadmission.SnapshotAcknowledgment{}, err
	}
	flightCtx, cancel := context.WithDeadline(ctx, time.Unix(0, g.ExpiresAtUnixNano))
	defer cancel()
	unlock, err := m.lockAppEgressPolicyForWake(flightCtx, g.Parent.Binding.AppID)
	if err != nil {
		return SnapshotInfo{}, runtimeadmission.SnapshotAcknowledgment{}, err
	}
	var gateOnce sync.Once
	releaseGate := func() { gateOnce.Do(unlock) }
	defer releaseGate()
	flight := &runtimeAdmissionFlight{ctx: flightCtx, cancel: cancel, done: make(chan struct{})}
	inst, err := m.consumeSnapshotGrant(g, flight)
	if err != nil {
		return SnapshotInfo{}, runtimeadmission.SnapshotAcknowledgment{}, err
	}
	defer m.finishRuntimeAdmissionFlight(g.Parent.Binding.InstanceID, flight)
	info, err := m.executeAdmittedSnapshot(flightCtx, inst, g)
	info, ack, err := m.finishAdmittedSnapshot(flightCtx, g, inst, flight, info, err)
	if err != nil {
		releaseGate()
		return m.failedAdmittedSnapshot(ctx, inst.Lease.Instance, err)
	}
	return info, ack, nil
}

func (m *Manager) checkSnapshotGrantIdentity(g runtimeadmission.SnapshotGrant) error {
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		return err
	}
	if g.Parent.Binding.NodeID != identity.NodeID || g.Parent.Binding.Incarnation != identity.Incarnation || identity.ProtocolVersion < runtimeadmission.ArtifactProtocolVersion || g.FCVersion != m.fcVersion {
		return runtimeadmission.ErrStale
	}
	return nil
}

func (m *Manager) executeAdmittedSnapshot(ctx context.Context, inst *Instance, g runtimeadmission.SnapshotGrant) (SnapshotInfo, error) {
	spec := SnapshotSpec{StorageKey: g.MemoryKey, VMStateStorageKey: g.VMStateKey, BeforeCheckpoint: g.BeforeCheckpoint, admittedGrant: &g}
	if g.Mode == "warm" {
		return m.WarmSnapshot(ctx, inst.Lease.Instance, spec)
	}
	return m.Park(ctx, inst.Lease.Instance, spec)
}

func (m *Manager) consumeSnapshotGrant(g runtimeadmission.SnapshotGrant, flight *runtimeAdmissionFlight) (*Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if err := g.Validate(now); err != nil {
		return nil, err
	}
	if err := flight.ctx.Err(); err != nil {
		return nil, err
	}
	b := g.Parent.Binding
	inst := m.live[b.InstanceID]
	if inst == nil || inst.Paused || inst.AppTaskOnly || inst.ExecutionOnly || !inst.runtimeAdmissionReceipt.Equal(g.Parent) || inst.AppID != b.AppID || inst.AccountID != b.AccountID || inst.DeploymentID != b.DeploymentID || inst.Lease.Instance != b.InstanceID || inst.Net.Netns != g.Parent.Netns || inst.Lease.HostIP.String() != g.Parent.HostIP || int32(inst.Lease.UID) != g.Parent.LeaseUID || m.vmm == nil {
		return nil, runtimeadmission.ErrStale
	}
	for token, expiry := range m.runtimeAdmissionTokens {
		if !now.Before(expiry) {
			delete(m.runtimeAdmissionTokens, token)
		}
	}
	if _, used := m.runtimeAdmissionTokens[g.Token]; used || m.runtimeAdmissionFlights[b.InstanceID] != nil {
		return nil, runtimeadmission.ErrReplay
	}
	if len(m.runtimeAdmissionTokens) >= api.ApplicationStandardRuntimeAdmissionReplayLimit {
		return nil, runtimeadmission.ErrCapacity
	}
	if err := m.checkOwnedRuntimeEgressLocked(inst.runtimeAdmissionEgress, b.EgressRevision); err != nil {
		return nil, err
	}
	m.runtimeAdmissionTokens[g.Token] = time.Unix(0, g.ExpiresAtUnixNano)
	m.runtimeAdmissionFlights[b.InstanceID] = flight
	return inst, nil
}

func (m *Manager) checkAdmittedSnapshotSpecLocked(instance string, spec SnapshotSpec, parent runtimeadmission.Receipt) error {
	g := spec.admittedGrant
	flight := m.runtimeAdmissionFlights[instance]
	if g == nil || flight == nil {
		return runtimeadmission.ErrUnavailable
	}
	if err := g.Validate(time.Now()); err != nil {
		return err
	}
	if err := flight.ctx.Err(); err != nil {
		return err
	}
	if !g.Parent.Equal(parent) || g.Parent.Binding.InstanceID != instance || g.FCVersion != m.fcVersion || g.MemoryKey != spec.StorageKey || g.VMStateKey != spec.VMStateStorageKey || g.BeforeCheckpoint != spec.BeforeCheckpoint {
		return runtimeadmission.ErrInvalid
	}
	return nil
}

func (m *Manager) finishAdmittedSnapshot(ctx context.Context, g runtimeadmission.SnapshotGrant, inst *Instance, flight *runtimeAdmissionFlight, info SnapshotInfo, err error) (SnapshotInfo, runtimeadmission.SnapshotAcknowledgment, error) {
	m.mu.Lock()
	now := time.Now()
	ack := runtimeadmission.SnapshotAcknowledgment{Grant: g.Clone(), Capture: info.Capture.Clone(), CompletedAtUnixNano: now.UnixNano()}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = ack.Check(g, now)
	}
	if err == nil && (m.runtimeAdmissionFlights[inst.Lease.Instance] != flight || g.Mode == "warm" && (m.live[inst.Lease.Instance] != inst || inst.Paused || !inst.runtimeAdmissionReceipt.Equal(g.Parent)) || g.Mode == "park" && m.live[inst.Lease.Instance] != nil) {
		err = runtimeadmission.ErrStale
	}
	m.finishRuntimeAdmissionFlightLocked(inst.Lease.Instance, flight)
	m.mu.Unlock()
	if err != nil {
		return SnapshotInfo{}, runtimeadmission.SnapshotAcknowledgment{}, err
	}
	info.Capture = info.Capture.Clone()
	return info, ack, nil
}

func (m *Manager) failedAdmittedSnapshot(ctx context.Context, instance string, err error) (SnapshotInfo, runtimeadmission.SnapshotAcknowledgment, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	return SnapshotInfo{}, runtimeadmission.SnapshotAcknowledgment{}, errors.Join(err, m.Destroy(cleanupCtx, instance))
}
