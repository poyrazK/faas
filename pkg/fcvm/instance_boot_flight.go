package fcvm

import (
	"context"
	"fmt"
)

// instanceBootFlight spans lease/network setup, artifact restoration and VMM boot.
// An instance is not in Manager.live during most of that time, so the ordinary
// Destroy lookup cannot see it. done closes only after Wake or BootJob has either
// published live or completed its error-path cleanup.
type instanceBootFlight struct {
	cancelled bool // guarded by Manager.mu
	cancel    context.CancelFunc
	done      chan struct{}
}

func (m *Manager) beginInstanceBoot(ctx context.Context, instance string) (context.Context, *instanceBootFlight, error) {
	bootCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.live[instance]; ok {
		cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: instance already live", instance)
	}
	if _, ok := m.instanceBoots[instance]; ok {
		cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: boot already in progress", instance)
	}
	if m.instanceBoots == nil {
		m.instanceBoots = make(map[string]*instanceBootFlight)
	}
	flight := &instanceBootFlight{cancel: cancel, done: make(chan struct{})}
	m.instanceBoots[instance] = flight
	return bootCtx, flight, nil
}

func (m *Manager) finishInstanceBoot(instance string, flight *instanceBootFlight) {
	m.mu.Lock()
	if m.instanceBoots[instance] == flight {
		delete(m.instanceBoots, instance)
	}
	close(flight.done)
	m.mu.Unlock()
	flight.cancel()
}

// cancelInFlightInstanceBoot is called before either Destroy or SignalAndKill
// checks Manager.live. It waits for the boot path to publish or unwind so the
// stop cannot race a late VMM return and leave customer code running.
func (m *Manager) cancelInFlightInstanceBoot(ctx context.Context, instance string) error {
	m.mu.Lock()
	flight := m.instanceBoots[instance]
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
		return fmt.Errorf("manager: wait for boot %s cancellation: %w", instance, ctx.Err())
	}
}
