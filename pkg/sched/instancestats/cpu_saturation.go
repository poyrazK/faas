package instancestats

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CPUSaturationHandler turns sustained CPU saturation into an automatic CPU
// capture (ADR-1155). Both methods run on the tick goroutine.
type CPUSaturationHandler interface {
	// CPUQuotaMillicores returns the instance's sustained cgroup CPU quota,
	// false while it is unknown or raised by a startup boost.
	CPUQuotaMillicores(instanceID string, now time.Time) (int, bool)
	// CPUSaturated runs once per saturation episode that lasted
	// api.ProfileCPUSaturationAfter. An error retries it on the next tick.
	CPUSaturated(ctx context.Context, row InstanceStat, usedMillicores, quotaMillicores int, sustained time.Duration) error
}

// cpuSaturationEpisode is one instance's current run of saturated samples.
type cpuSaturationEpisode struct {
	since, last time.Time
	acted       bool
}

// WithCPUSaturationHandler enables automatic CPU captures of saturated
// instances. Nil keeps the poller observation-only.
func (p *Poller) WithCPUSaturationHandler(handler CPUSaturationHandler) *Poller {
	if p != nil {
		p.CPUSaturationHandler = handler
	}
	return p
}

// cpuSaturated reports whether usedMillicores is at least
// api.ProfileCPUSaturationPercent of quotaMillicores.
func cpuSaturated(usedMillicores, quotaMillicores int) bool {
	return quotaMillicores > 0 && usedMillicores*100 >= quotaMillicores*api.ProfileCPUSaturationPercent
}

// watchCPUSaturation calls CPUSaturationHandler once per instance whose CPU
// use stays saturated for api.ProfileCPUSaturationAfter. Dips shorter than
// api.ProfileCPUSaturationGrace keep the episode; a longer dip, an unknown
// quota or leaving the snapshot ends it and re-arms the instance.
func (p *Poller) watchCPUSaturation(ctx context.Context, rows []InstanceStat) {
	if p == nil || p.CPUSaturationHandler == nil {
		return
	}
	type action struct {
		row         InstanceStat
		used, quota int
		sustained   time.Duration
	}
	now := p.now()
	var actions []action
	p.cpuSaturationMu.Lock()
	if p.cpuSaturation == nil {
		p.cpuSaturation = make(map[string]*cpuSaturationEpisode)
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.InstanceID == "" {
			continue
		}
		seen[row.InstanceID] = struct{}{}
		if row.CPU != Valid {
			continue
		}
		quota, ok := p.CPUSaturationHandler.CPUQuotaMillicores(row.InstanceID, now)
		if !ok {
			delete(p.cpuSaturation, row.InstanceID)
			continue
		}
		used := int(row.CPUPct * 10) // CPUPct is percent of one CPU
		episode := p.cpuSaturation[row.InstanceID]
		if !cpuSaturated(used, quota) {
			if episode != nil && now.Sub(episode.last) > api.ProfileCPUSaturationGrace {
				delete(p.cpuSaturation, row.InstanceID)
			}
			continue
		}
		if episode == nil {
			episode = &cpuSaturationEpisode{since: now}
			p.cpuSaturation[row.InstanceID] = episode
		}
		episode.last = now
		if !episode.acted && now.Sub(episode.since) >= api.ProfileCPUSaturationAfter {
			actions = append(actions, action{row: row, used: used, quota: quota, sustained: now.Sub(episode.since)})
		}
	}
	for instanceID := range p.cpuSaturation {
		if _, ok := seen[instanceID]; !ok {
			delete(p.cpuSaturation, instanceID)
		}
	}
	p.cpuSaturationMu.Unlock()
	for _, a := range actions {
		if err := p.CPUSaturationHandler.CPUSaturated(ctx, a.row, a.used, a.quota, a.sustained); err != nil {
			if p.Log != nil {
				p.Log.Warn("instance stats: cpu saturation handler failed", "instance_id", a.row.InstanceID, "app_id", a.row.AppID,
					"used_millicores", a.used, "quota_millicores", a.quota, "err", err)
			}
			continue
		}
		p.cpuSaturationMu.Lock()
		if episode := p.cpuSaturation[a.row.InstanceID]; episode != nil {
			episode.acted = true
		}
		p.cpuSaturationMu.Unlock()
	}
}
