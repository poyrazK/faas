package fcvm

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/frameworkready"
)

type frameworkReadyRun struct {
	cancel   context.CancelFunc
	done     chan struct{}
	instance *Instance
}

// WithFrameworkReadyReader wires a bounded per-VM read; the Manager owns its lifecycle.
func (m *Manager) WithFrameworkReadyReader(read func(context.Context, string) (frameworkready.Status, error)) *Manager {
	m.frameworkReadyReader = read
	return m
}
func (m *Manager) startFrameworkReadyLoop(ctx context.Context, id string) {
	m.mu.Lock()
	inst := m.live[id]
	if inst == nil || inst.Runtime == "" || m.frameworkReadyReader == nil || m.frameworkReadyRuns[id] != nil {
		m.mu.Unlock()
		return
	}
	parent := m.lifecycleCtx //nolint:contextcheck // Observer belongs to the daemon, not the short-lived wake RPC.
	if parent == nil {
		parent = context.WithoutCancel(ctx)
	}
	ctx, cancel := context.WithCancel(parent)
	run := &frameworkReadyRun{cancel: cancel, done: make(chan struct{}), instance: inst}
	if m.frameworkReadyRuns == nil {
		m.frameworkReadyRuns = make(map[string]*frameworkReadyRun)
	}
	m.frameworkReadyRuns[id] = run
	m.mu.Unlock()
	go func() {
		defer close(run.done)
		defer cancel()
		defer func() {
			m.mu.Lock()
			if m.frameworkReadyRuns[id] == run {
				delete(m.frameworkReadyRuns, id)
			}
			m.mu.Unlock()
		}()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		lastError := ""
		for {
			callCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			ready, err := m.frameworkReadyReader(callCtx, id)
			if err == nil && ready.Ready && callCtx.Err() == nil {
				m.mu.Lock()
				current := m.live[id] == run.instance && m.frameworkReadyRuns[id] == run
				m.mu.Unlock()
				if !current {
					stop()
					return
				}
				var stamped bool
				stamped, _, _, err = m.MarkInstanceFrameworkReady(callCtx, id, int64(ready.WarmupMs))
				if err == nil && stamped {
					m.log.Info("framework-ready recorded", "instance", id)
					stop()
					return
				}
			}
			if err != nil && ctx.Err() == nil && err.Error() != lastError {
				lastError = err.Error()
				m.log.Warn("framework-ready receipt pending", "instance", id, "err", err)
			}
			stop()
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}
func (m *Manager) cancelFrameworkReadyLoop(id string) {
	m.mu.Lock()
	run := m.frameworkReadyRuns[id]
	delete(m.frameworkReadyRuns, id)
	m.mu.Unlock()
	if run != nil {
		run.cancel()
		<-run.done
	}
}

// ReadFrameworkReady uses the same instance-bound Firecracker bridge as resume.
// There is no host AF_VSOCK bind or guest-supplied instance identity.
func ReadFrameworkReady(ctx context.Context, socket string) (frameworkready.Status, error) {
	var zero frameworkready.Status
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(callCtx, "unix", socket)
	if err != nil {
		return zero, err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(callCtx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := callCtx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return zero, err
	}
	if _, err = fmt.Fprintf(conn, "CONNECT %d\n", frameworkready.Port); err != nil {
		return zero, err
	}
	ack, err := readConnectAck(conn)
	if err != nil {
		return zero, err
	}
	if ack != "OK" {
		return zero, fmt.Errorf("framework-ready CONNECT rejected: %q", ack)
	}
	return frameworkready.Read(conn)
}
