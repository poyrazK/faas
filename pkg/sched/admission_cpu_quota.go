package sched

import "time"

// SustainedCPUMillicores returns the sustained cgroup CPU quota admitted for
// the instance, for CPU saturation captures (ADR-1155). It reports false for
// an unknown or frozen instance and while a startup boost raises the quota,
// because use above the sustained quota is expected then.
func (l *NodeLedger) SustainedCPUMillicores(instance string, now time.Time) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.entries[instance]
	if !ok || r.frozen || r.cpuMillicores <= 0 || r.cpuBoostMillicores > 0 && now.Before(r.cpuBoostUntil) {
		return 0, false
	}
	return r.cpuMillicores, true
}
