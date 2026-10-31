package fcvm

// Agent run usage (ADR-20261027090011 Measurement). While RunAgent runs, vmmd
// samples the run VM's private memory, anon + shmem of its cgroup's
// memory.stat, every api.AgentRunUsageSampleMillis. It splits each interval's
// time by the VM's quiet pause ledger, so the time it spent paused waiting on
// the model (ADR-20261027090012) is counted apart. The record is reported
// once, when the exchange ends. Nothing bills it yet (rollout step 2).
//
// Shared snapshot pages are page cache charged to whichever VM touched them
// first, so memory.current is not measured: only private pages are.

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AgentRunUsageMethod versions the rules below. A record carries the method
// it was made with, so a later rule never reinterprets an older run.
const AgentRunUsageMethod = 1

// AgentRunUsage is one run's measured memory. MB is MiB, as in ram_mb.
type AgentRunUsage struct {
	Method int
	// Samples counts the intervals whose memory was read; ExpectedSamples
	// counts every interval. An interval whose read failed adds time but no
	// memory.
	Samples, ExpectedSamples int
	// MeasuredMillis spans the first sample's baseline to the last sample;
	// PausedMillis is the part of it the VM spent quiet-paused.
	MeasuredMillis, PausedMillis int64
	// PrivateMBMillis sums private MB × ms over the read intervals, and
	// UnpausedPrivateMBMillis only over their unpaused time.
	PrivateMBMillis, UnpausedPrivateMBMillis int64
	// BillableMBMillis applies the ADR's per-second rate to the unpaused time
	// of the read intervals: max(private, AgentRunUsageFloorMB) + 8 MB.
	BillableMBMillis int64
	PeakPrivateMB    int64
	// ColdBoot is true when the VM booted its image rather than restoring a
	// snapshot (the ADR's cold-boot fallback).
	ColdBoot bool
}

// AgentRunMemory reads a run VM's private memory in bytes. JailerVMM
// implements it.
type AgentRunMemory interface {
	VMPrivateBytes(Lease) (int64, error)
}

// VMPrivateBytes is the VM cgroup's anon + shmem from memory.stat.
func (v *JailerVMM) VMPrivateBytes(l Lease) (int64, error) {
	stat, err := readCgroupStat(vmCgroupScope(l))
	if err != nil {
		return 0, err
	}
	return stat["anon"] + stat["shmem"], nil
}

// agentUsageMeter accumulates one run's usage. Only its own goroutine and,
// after that goroutine ends, the stop function touch it.
type agentUsageMeter struct {
	read   func(Lease) (int64, error)
	paused func(now time.Time) time.Duration
	lease  Lease

	started, last           time.Time
	lastPaused, pausedTotal time.Duration
	usage                   AgentRunUsage
}

func newAgentUsageMeter(read func(Lease) (int64, error), paused func(time.Time) time.Duration, lease Lease, coldBoot bool, now time.Time) *agentUsageMeter {
	return &agentUsageMeter{read: read, paused: paused, lease: lease, started: now, last: now, lastPaused: paused(now),
		usage: AgentRunUsage{Method: AgentRunUsageMethod, ColdBoot: coldBoot}}
}

// sample closes the interval that ends at now, valued at the memory read now.
func (u *agentUsageMeter) sample(now time.Time) {
	dt := now.Sub(u.last)
	if dt <= 0 {
		return
	}
	p := u.paused(now)
	dPaused := min(max(p-u.lastPaused, 0), dt)
	u.last, u.lastPaused = now, max(p, u.lastPaused)
	u.pausedTotal += dPaused
	u.usage.ExpectedSamples++
	u.usage.MeasuredMillis = now.Sub(u.started).Milliseconds()
	u.usage.PausedMillis = u.pausedTotal.Milliseconds()
	bytes, err := u.read(u.lease)
	if err != nil || bytes < 0 {
		return
	}
	u.usage.Samples++
	ms, unpausedMS := dt.Milliseconds(), (dt - dPaused).Milliseconds()
	u.usage.PrivateMBMillis += bytes * ms >> 20
	u.usage.UnpausedPrivateMBMillis += bytes * unpausedMS >> 20
	billable := max(bytes, int64(api.AgentRunUsageFloorMB)<<20) + int64(api.PerVMOverheadMB)<<20
	u.usage.BillableMBMillis += billable * unpausedMS >> 20
	u.usage.PeakPrivateMB = max(u.usage.PeakPrivateMB, bytes>>20)
}

// agentUsagePaused is the VM's quiet-paused time so far: its completed
// pauses and the current one.
func (m *Manager) agentUsagePaused(instance string) func(time.Time) time.Duration {
	return func(now time.Time) time.Duration {
		r := m.QuietPauseReport(instance)
		total := r.PausedTotal
		if r.Paused && !r.Since.IsZero() && now.After(r.Since) {
			total += now.Sub(r.Since)
		}
		return total
	}
}

// agentUsageRun starts measuring a run VM and returns the function that takes
// the last sample, stops measuring and returns the record. A VMM that cannot
// read private memory measures nothing, and the function returns nil.
func (m *Manager) agentUsageRun(instance string, inst *Instance) func() *AgentRunUsage {
	reader, ok := m.vmm.(AgentRunMemory)
	if !ok || inst == nil {
		return func() *AgentRunUsage { return nil }
	}
	meter := newAgentUsageMeter(reader.VMPrivateBytes, m.agentUsagePaused(instance), inst.Lease,
		inst.Method == WakeColdBoot, time.Now())
	tick := m.agentUsageTick
	if tick <= 0 {
		tick = api.AgentRunUsageSampleMillis * time.Millisecond
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				meter.sample(now)
			}
		}
	}()
	return func() *AgentRunUsage {
		close(stop)
		<-done
		meter.sample(time.Now())
		usage := meter.usage
		return &usage
	}
}
