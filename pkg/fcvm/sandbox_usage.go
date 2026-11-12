package fcvm

// Sandbox session usage (ADR-20261027090013 §5, ADR-20261027090011). A
// session VM is measured like an agent run VM, for its whole life: private
// memory sampled every api.AgentRunUsageSampleMillis. Quiet-paused and
// frozen seconds are not billed (owner decision D15), so both count as
// paused; a freeze first ends any quiet pause, so the two never overlap.
// A session lives for hours, so vmmd reads the record while it runs and
// reports it cumulatively. Nothing bills it yet (ADR-20261027090013 step 11).

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// SandboxUsageMeter measures one session VM. Snapshot is the record so far;
// Stop takes the last sample, stops measuring and returns the final record.
// Both are safe to call from any goroutine; Stop may be called once.
type SandboxUsageMeter interface {
	Snapshot() AgentRunUsage
	Stop() AgentRunUsage
}

// SandboxUsage is the Manager's SandboxUsageMeter.
type SandboxUsage struct {
	meter *agentUsageMeter
	stop  chan struct{}
	done  chan struct{}
}

// Snapshot is the session's usage so far.
func (s *SandboxUsage) Snapshot() AgentRunUsage {
	if s == nil {
		return AgentRunUsage{}
	}
	return s.meter.snapshot()
}

// Stop ends the measurement and returns the final record.
func (s *SandboxUsage) Stop() AgentRunUsage {
	if s == nil {
		return AgentRunUsage{}
	}
	close(s.stop)
	<-s.done
	s.meter.sample(time.Now())
	return s.meter.snapshot()
}

// MeterSandbox starts measuring a session VM. It returns nil when the VMM
// cannot read private memory or the instance is not live.
func (m *Manager) MeterSandbox(instance string) SandboxUsageMeter {
	reader, ok := m.vmm.(AgentRunMemory)
	m.mu.Lock()
	inst := m.live[instance]
	m.mu.Unlock()
	if !ok || inst == nil {
		return nil
	}
	quiet := m.agentUsagePaused(instance)
	frozen := func(now time.Time) time.Duration { return m.FrozenTime(instance, now) }
	paused := func(now time.Time) time.Duration { return quiet(now) + frozen(now) }
	meter := newAgentUsageMeter(reader.VMPrivateBytes, paused, inst.Lease, inst.Method == WakeColdBoot, time.Now())
	meter.frozen = frozen
	tick := m.agentUsageTick
	if tick <= 0 {
		tick = api.AgentRunUsageSampleMillis * time.Millisecond
	}
	u := &SandboxUsage{meter: meter, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(u.done)
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-u.stop:
				return
			case now := <-ticker.C:
				meter.sample(now)
			}
		}
	}()
	return u
}
