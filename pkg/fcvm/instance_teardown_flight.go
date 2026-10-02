package fcvm

import (
	"context"
	"fmt"
)

// Stops join completion of the owner's process and Manager cleanup steps.
// Removing live alone cannot acknowledge that boundary to another caller.
type instanceTeardownFlight struct {
	done       chan struct{}
	exitCode   int
	killSignal bool
	err        error
	exportDir  string
	// Builder interruption leaves export/cleanup with the destroy owner.
	interruptOnly bool
}

func (m *Manager) beginInstanceTeardown(ctx context.Context, instance, exportDir string) (*instanceTeardownFlight, bool, error) {
	return m.joinInstanceTeardown(ctx, instance, exportDir, nil)
}

// Park and the unexpected-exit fallback already observed a live incarnation.
// Do not let a late caller retire a replacement using the same identity.
func (m *Manager) beginLiveInstanceTeardown(ctx context.Context, instance string, expected *Instance) (*instanceTeardownFlight, bool, error) {
	return m.joinInstanceTeardown(ctx, instance, "", expected)
}

func (m *Manager) joinInstanceTeardown(ctx context.Context, instance, exportDir string, expected *Instance) (*instanceTeardownFlight, bool, error) {
	m.mu.Lock()
	flight := m.teardowns[instance]
	if flight == nil {
		if expected != nil && m.live[instance] != expected {
			m.mu.Unlock()
			return nil, false, fmt.Errorf("manager: instance %s incarnation changed before teardown", instance)
		}
		if m.teardowns == nil {
			m.teardowns = make(map[string]*instanceTeardownFlight)
		}
		flight = &instanceTeardownFlight{done: make(chan struct{}), exportDir: exportDir}
		m.teardowns[instance] = flight
		m.mu.Unlock()
		return flight, true, nil
	}
	m.mu.Unlock()
	select {
	case <-flight.done:
		return flight, false, nil
	case <-ctx.Done():
		return nil, false, fmt.Errorf("manager: wait for instance %s teardown: %w", instance, ctx.Err())
	}
}

func (m *Manager) finishInstanceTeardown(instance string, flight *instanceTeardownFlight, exitCode int, killSignal, interruptOnly bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	flight.exitCode, flight.killSignal, flight.err = exitCode, killSignal, err
	flight.interruptOnly = interruptOnly
	if m.teardowns[instance] == flight {
		delete(m.teardowns, instance)
	}
	close(flight.done)
}

func (m *Manager) interruptExportingBuilder(ctx context.Context, instance string) (bool, bool, int32, error) {
	m.mu.Lock()
	registered := m.exportDirs[instance] != ""
	m.mu.Unlock()
	if !registered {
		return false, false, 0, nil
	}
	if interrupter, ok := m.vmm.(interface {
		InterruptBuild(context.Context, string) (int32, error)
	}); ok {
		code, err := interrupter.InterruptBuild(ctx, instance)
		return true, true, code, err
	}
	return true, false, 0, fmt.Errorf("vmm: builder interruption unsupported")
}
