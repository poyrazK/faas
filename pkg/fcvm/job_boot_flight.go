package fcvm

import (
	"context"
	"fmt"
)

// jobBootFlight spans lease/network setup, artifact restoration and VMM boot.
// A job is not in Manager.live during most of that time, so the ordinary
// Destroy lookup cannot see it. done closes only after BootJob has either
// published live or completed its error-path cleanup.
type jobBootFlight struct {
	cancelled bool // guarded by Manager.mu
	cancel    context.CancelFunc
	done      chan struct{}
}

func (m *Manager) beginJobBoot(ctx context.Context, instance string) (context.Context, *jobBootFlight, error) {
	bootCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.live[instance]; ok {
		cancel()
		return nil, nil, fmt.Errorf("manager: BootJob %s: instance already live", instance)
	}
	if _, ok := m.jobBoots[instance]; ok {
		cancel()
		return nil, nil, fmt.Errorf("manager: BootJob %s: boot already in progress", instance)
	}
	if m.jobBoots == nil {
		m.jobBoots = make(map[string]*jobBootFlight)
	}
	flight := &jobBootFlight{cancel: cancel, done: make(chan struct{})}
	m.jobBoots[instance] = flight
	return bootCtx, flight, nil
}

func (m *Manager) finishJobBoot(instance string, flight *jobBootFlight) {
	m.mu.Lock()
	if m.jobBoots[instance] == flight {
		delete(m.jobBoots, instance)
	}
	close(flight.done)
	m.mu.Unlock()
	flight.cancel()
}

// cancelInFlightJobBoot is called before either Destroy or SignalAndKill
// checks Manager.live. It waits for the boot path to publish or unwind so the
// stop cannot race a late VMM return and leave customer code running.
func (m *Manager) cancelInFlightJobBoot(ctx context.Context, instance string) error {
	m.mu.Lock()
	flight := m.jobBoots[instance]
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
		return fmt.Errorf("manager: wait for job boot %s cancellation: %w", instance, ctx.Err())
	}
}
