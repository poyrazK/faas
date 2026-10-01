package fcvm

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// PromoteAdmitted resumes only the exact paused lease described by the saved
// parent receipt. The app policy gate spans validation, resume and receipt.
func (m *Manager) PromoteAdmitted(ctx context.Context, p runtimeadmission.Promotion) (*Instance, runtimeadmission.Receipt, error) {
	if err := p.Validate(time.Now()); err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	if p.Binding.NodeID != identity.NodeID || p.Binding.Incarnation != identity.Incarnation {
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrStale
	}
	flightCtx, cancel := context.WithDeadline(ctx, time.Unix(0, p.Binding.ExpiresAtUnixNano))
	defer cancel()
	unlock, err := m.lockAppEgressPolicyForWake(flightCtx, p.Binding.AppID)
	if err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	defer unlock()
	flight := &runtimeAdmissionFlight{ctx: flightCtx, cancel: cancel, done: make(chan struct{})}
	inst, err := m.consumeRuntimePromotion(p, flight)
	if err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	defer m.finishRuntimeAdmissionFlight(p.Binding.InstanceID, flight)
	err = m.vmm.ResumeVM(flightCtx, inst.Lease)
	if err == nil {
		// KeepPaused skipped the restore hook. Complete entropy reseeding and
		// clock correction before monitors or serving publication can start.
		err = m.vmm.TriggerResumeHook(flightCtx, inst.Lease, time.Now().UnixNano())
	}
	if err == nil {
		m.mu.Lock()
		if m.live[p.Binding.InstanceID] != inst || !inst.Paused || inst.runtimeAdmissionReceipt != p.Parent {
			err = runtimeadmission.ErrStale
		} else {
			inst.Paused = false
		}
		m.mu.Unlock()
	}
	if err == nil {
		m.startLivenessLoop(context.WithoutCancel(ctx), p.Binding.InstanceID, inst.Lease.Slot, inst.LivenessProbe)
		m.startReadinessLoop(context.WithoutCancel(ctx), p.Binding.InstanceID, inst.Lease.Slot, inst.ReadinessProbe)
		m.startFrameworkReadyLoop(context.WithoutCancel(ctx), p.Binding.InstanceID)
	}
	m.mu.Lock()
	completed := time.Now()
	if err == nil {
		err = p.Validate(completed)
	}
	if err == nil {
		err = flightCtx.Err()
	}
	if err == nil && (m.live[p.Binding.InstanceID] != inst || inst.Paused || inst.runtimeAdmissionReceipt != p.Parent) {
		err = runtimeadmission.ErrStale
	}
	if err != nil {
		m.finishRuntimeAdmissionFlightLocked(p.Binding.InstanceID, flight)
		m.mu.Unlock()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		defer cleanupCancel()
		return nil, runtimeadmission.Receipt{}, errors.Join(err, m.Destroy(cleanupCtx, p.Binding.InstanceID))
	}
	r := p.Parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, completed.UnixNano()
	inst.Paused, inst.runtimeAdmissionReceipt = false, r
	m.finishRuntimeAdmissionFlightLocked(p.Binding.InstanceID, flight)
	m.mu.Unlock()
	return inst, r, nil
}

func (m *Manager) consumeRuntimePromotion(p runtimeadmission.Promotion, flight *runtimeAdmissionFlight) (*Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if err := p.Validate(now); err != nil {
		return nil, err
	}
	if err := flight.ctx.Err(); err != nil {
		return nil, err
	}
	inst := m.live[p.Binding.InstanceID]
	if inst == nil || !inst.Paused || inst.AppTaskOnly || inst.ExecutionOnly || inst.runtimeAdmissionReceipt != p.Parent || inst.Lease.Instance != p.Binding.InstanceID || inst.AppID != p.Binding.AppID || inst.AccountID != p.Binding.AccountID || inst.DeploymentID != p.Binding.DeploymentID || inst.Net.Netns != p.Parent.Netns || inst.Lease.HostIP.String() != p.Parent.HostIP || int32(inst.Lease.UID) != p.Parent.LeaseUID || m.vmm == nil {
		return nil, runtimeadmission.ErrStale
	}
	for token, expiry := range m.runtimeAdmissionTokens {
		if !now.Before(expiry) {
			delete(m.runtimeAdmissionTokens, token)
		}
	}
	if _, used := m.runtimeAdmissionTokens[p.Binding.Token]; used || m.runtimeAdmissionFlights[p.Binding.InstanceID] != nil {
		return nil, runtimeadmission.ErrReplay
	}
	if len(m.runtimeAdmissionTokens) >= api.ApplicationStandardRuntimeAdmissionReplayLimit {
		return nil, runtimeadmission.ErrCapacity
	}
	input := inst.runtimeAdmissionEgress
	input.admission = &p.Binding
	if err := m.checkAdmittedEgressLocked(input); err != nil {
		return nil, err
	}
	// An initial admitted boot creates these maps before it can produce parent.
	m.runtimeAdmissionTokens[p.Binding.Token] = time.Unix(0, p.Binding.ExpiresAtUnixNano)
	m.runtimeAdmissionFlights[p.Binding.InstanceID] = flight
	return inst, nil
}
