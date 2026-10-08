package fcvm

import (
	"context"
	"fmt"
)

// instanceFlight spans boot or a live operation that can resume a guest.
// Destroy must join it before teardown. A boot is invisible to Manager.live
// during lease/network setup and artifact restoration. done closes after the
// operation returns, including boot cleanup and post-publication hooks.
type instanceFlight struct {
	cancelled bool // guarded by Manager.mu
	// parking marks a Park flight: VMM.Snapshot kills Firecracker itself
	// after the capture, before cleanup registers the teardown, so that
	// exit is expected and must not be relayed as a liveness failure.
	parking bool // guarded by Manager.mu
	cancel  context.CancelFunc
	done    chan struct{}
	// Recovery ignores an expired RPC but is cancelled by Destroy and finish.
	recoveryCtx context.Context
}

func newInstanceFlight(ctx context.Context) (context.Context, *instanceFlight) {
	operationCtx, cancelOperation := context.WithCancel(ctx)
	recoveryCtx, cancelRecovery := context.WithCancel(context.WithoutCancel(ctx))
	flight := &instanceFlight{
		done: make(chan struct{}), recoveryCtx: recoveryCtx,
		cancel: func() {
			// The operation can unwind as soon as cancellation wakes it.
			// Close recovery first so a failed snapshot cannot start a resume
			// between the two cancellations during teardown.
			cancelRecovery()
			cancelOperation()
		},
	}
	return operationCtx, flight
}

func (m *Manager) beginInstanceBoot(ctx context.Context, instance string) (context.Context, *instanceFlight, error) {
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return nil, nil, fmt.Errorf("manager: native ownership recovery: %w", err)
	}
	if v := m.nativeVMM(); v != nil {
		if err := v.nativeRecoveryRuntime().journal.checkQualificationProducer(ctx, instance); err != nil {
			return nil, nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	bootCtx, flight := newInstanceFlight(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, held := m.restartQuarantine[instance]; held {
		flight.cancel()
		return nil, nil, fmt.Errorf("boot %s: %w", instance, ErrRestartQuarantine)
	}
	if _, ok := m.instanceFlights[instance]; ok {
		flight.cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: boot already in progress", instance)
	}
	if m.instanceStops[instance] != nil {
		flight.cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: teardown in progress", instance)
	}
	if m.pendingCleanup[instance] != nil {
		flight.cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: teardown pending", instance)
	}
	if _, ok := m.live[instance]; ok {
		flight.cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: instance already live", instance)
	}
	if m.instanceFlights == nil {
		m.instanceFlights = make(map[string]*instanceFlight)
	}
	m.instanceFlights[instance] = flight
	return bootCtx, flight, nil
}

// beginLiveInstanceFlight atomically pins the live identity and registers a
// resumable operation before checking admission or calling the VMM. It shares
// the boot registry so Destroy cannot overlook a request between lookup and RPC.
func (m *Manager) beginLiveInstanceFlight(ctx context.Context, instance string) (context.Context, *Instance, *instanceFlight, error) {
	if v := m.nativeVMM(); v != nil {
		if err := v.nativeRecoveryRuntime().journal.checkQualificationProducer(ctx, instance); err != nil {
			return nil, nil, nil, err
		}
	}
	operationCtx, flight := newInstanceFlight(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, held := m.restartQuarantine[instance]; held {
		flight.cancel()
		return nil, nil, nil, fmt.Errorf("operate on %s: %w", instance, ErrRestartQuarantine)
	}
	if m.instanceStops[instance] != nil || m.pendingCleanup[instance] != nil {
		flight.cancel()
		return nil, nil, nil, fmt.Errorf("instance teardown in progress")
	}
	inst, ok := m.live[instance]
	if !ok {
		flight.cancel()
		return nil, nil, nil, fmt.Errorf("instance is not live")
	}
	if _, ok := m.instanceFlights[instance]; ok {
		flight.cancel()
		return nil, nil, nil, fmt.Errorf("instance operation already in progress")
	}
	if m.instanceFlights == nil {
		m.instanceFlights = make(map[string]*instanceFlight)
	}
	m.instanceFlights[instance] = flight
	return operationCtx, inst, flight, nil
}

func (m *Manager) finishInstanceFlight(instance string, flight *instanceFlight) {
	m.mu.Lock()
	if m.instanceFlights[instance] == flight {
		delete(m.instanceFlights, instance)
	}
	close(flight.done)
	m.mu.Unlock()
	flight.cancel()
}

// cancelInFlightInstance is called before either Destroy or SignalAndKill
// checks Manager.live. It waits for boot/resume to publish or unwind so the
// stop cannot race a late VMM return and leave customer code running.
func (m *Manager) cancelInFlightInstance(ctx context.Context, instance string) error {
	m.mu.Lock()
	flight := m.instanceFlights[instance]
	if flight != nil {
		flight.cancelled = true
	}
	m.mu.Unlock()
	if flight == nil {
		return nil
	}
	flight.cancel()
	select {
	case <-flight.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("manager: wait for instance %s cancellation: %w", instance, ctx.Err())
	}
}

func (m *Manager) hasInstanceOperation(instance string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instanceFlights[instance] != nil
}
