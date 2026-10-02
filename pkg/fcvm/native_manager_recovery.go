package fcvm

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/netns"
)

type nativeRecoveryVMM interface {
	nativeRecoveryRuntime() *nativeProcessRecoveryRuntime
	nativeRecoveryLeases(context.Context) ([]Lease, error)
	prepareNativeLease(context.Context, Lease) error
	confirmNativeCleanup(context.Context, Lease, netns.Config) error
}

type nativeRecoveryInitFlight struct {
	done chan struct{}
	err  error
}

func (m *Manager) nativeVMM() nativeRecoveryVMM {
	v, ok := m.vmm.(nativeRecoveryVMM)
	if !ok || v.nativeRecoveryRuntime() == nil {
		return nil
	}
	return v
}

// RecoverNativeProcesses quarantines ownership only. Recovered tasks never
// enter live, the CID index, readiness, or a scheduler qualification receipt.
// Admission and prepared-network allocation also call this gate themselves.
func (m *Manager) RecoverNativeProcesses(ctx context.Context) error {
	if checker, ok := m.vmm.(interface{ checkNativeRecoveryMode() error }); ok {
		if err := checker.checkNativeRecoveryMode(); err != nil {
			return err
		}
	}
	v := m.nativeVMM()
	if v == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.nativeRecoveryReady {
		m.mu.Unlock()
		return nil
	}
	if flight := m.nativeRecoveryFlight; flight != nil {
		m.mu.Unlock()
		select {
		case <-flight.done:
			return flight.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	flight := &nativeRecoveryInitFlight{done: make(chan struct{})}
	m.nativeRecoveryFlight = flight
	m.mu.Unlock()
	leases, err := v.nativeRecoveryLeases(ctx)
	if err == nil {
		err = m.alloc.reserveRecovered(leases)
	}
	m.mu.Lock()
	if err == nil {
		m.nativeRecovered = make(map[string][]Lease)
		for _, lease := range leases {
			m.nativeRecovered[lease.Instance] = append(m.nativeRecovered[lease.Instance], lease)
		}
		m.nativeRecoveryReady = true
	}
	flight.err = err
	m.nativeRecoveryFlight = nil
	close(flight.done)
	m.mu.Unlock()
	return err
}

func (m *Manager) prepareNativeLease(ctx context.Context, lease Lease) error {
	if v := m.nativeVMM(); v != nil {
		return v.prepareNativeLease(ctx, lease)
	}
	return nil
}

// The in-memory absence of an instance after daemon death is not a stop ACK.
// A journal frame supplies its owned lease; a full retirement and resource
// proof are required before the allocator releases its quarantined slot.
func (m *Manager) retireUnknownNative(ctx context.Context, instance, exportDir string) (int, error) {
	v := m.nativeVMM()
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return -1, err
	}
	record, err := v.nativeRecoveryRuntime().journal.read(instance)
	if err != nil {
		return -1, fmt.Errorf("native recovery: unknown stop has no launch ownership: %w", err)
	}
	m.mu.Lock()
	leases := append([]Lease(nil), m.nativeRecovered[instance]...)
	m.mu.Unlock()
	for _, lease := range leases {
		if lease.Slot != record.Lease.Slot {
			return -1, errors.New("native recovery: duplicate instance slots require explicit recovery")
		}
	}
	if exportDir != "" {
		return m.vmm.DestroyWithExport(ctx, record.Lease, exportDir)
	}
	if err := m.cleanup(ctx, record.Lease, nativeLeaseNetwork(record.Lease), nil); err != nil {
		return -1, err
	}
	return -1, nil // No historical guest exit status survived this daemon.
}

func (m *Manager) releaseNativeRecovered(instance string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.nativeRecovered[instance]) == 0 {
		return false, nil
	}
	if err := m.alloc.releaseRecovered(instance); err != nil {
		return true, err
	}
	delete(m.nativeRecovered, instance)
	return true, nil
}
