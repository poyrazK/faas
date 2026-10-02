package fcvm

import (
	"context"
	"fmt"
)

// instanceBootFlight spans lease/network setup, artifact restoration and VMM
// boot. The instance is not in Manager.live during most of that time, so the
// ordinary Destroy lookup cannot see it. done closes only after boot has either
// published live or completed its error-path cleanup.
type instanceBootFlight struct {
	cancelled bool // guarded by Manager.mu
	cancel    context.CancelFunc
	done      chan struct{}
}

func (m *Manager) beginInstanceBoot(ctx context.Context, instance string) (context.Context, *instanceBootFlight, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("manager: boot %s: %w", instance, err)
	}
	bootCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := bootCtx.Err(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: %w", instance, err)
	}
	if _, ok := m.live[instance]; ok {
		cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: instance already live", instance)
	}
	if _, ok := m.bootFlights[instance]; ok {
		cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: boot already in progress", instance)
	}
	if _, ok := m.teardowns[instance]; ok {
		cancel()
		return nil, nil, fmt.Errorf("manager: boot %s: teardown in progress", instance)
	}
	if m.bootFlights == nil {
		m.bootFlights = make(map[string]*instanceBootFlight)
	}
	flight := &instanceBootFlight{cancel: cancel, done: make(chan struct{})}
	m.bootFlights[instance] = flight
	return bootCtx, flight, nil
}

func (m *Manager) finishInstanceBoot(instance string, flight *instanceBootFlight) {
	m.mu.Lock()
	if m.bootFlights[instance] == flight {
		delete(m.bootFlights, instance)
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
	flight := m.bootFlights[instance]
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
		return fmt.Errorf("manager: wait for instance boot %s cancellation: %w", instance, ctx.Err())
	}
}
