package instancestats

import (
	"context"
	"sync"
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

// cpuEpisodes tracks per-instance CPU saturation episodes for one policy
// (ADR-1155 captures, ADR-20261112120603 abuse).
type cpuEpisodes struct {
	mu       sync.Mutex
	episodes map[string]*cpuSaturationEpisode
}

// cpuEpisodePolicy is the threshold, duration and grace of one policy.
type cpuEpisodePolicy struct {
	percent      int
	after, grace time.Duration
}

// cpuEpisodeAction is an instance whose episode reached the policy's
// duration and has not been acted on.
type cpuEpisodeAction struct {
	row         InstanceStat
	used, quota int
	sustained   time.Duration
}

// due advances every instance's episode with this tick's rows and returns
// the instances due an action. Dips shorter than the grace keep the episode;
// a longer dip, an unknown quota or leaving the snapshot ends it and re-arms
// the instance.
func (e *cpuEpisodes) due(rows []InstanceStat, now time.Time, quotaOf func(string, time.Time) (int, bool), policy cpuEpisodePolicy) []cpuEpisodeAction {
	var actions []cpuEpisodeAction
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.episodes == nil {
		e.episodes = make(map[string]*cpuSaturationEpisode)
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
		quota, ok := quotaOf(row.InstanceID, now)
		if !ok {
			delete(e.episodes, row.InstanceID)
			continue
		}
		used := int(row.CPUPct * 10) // CPUPct is percent of one CPU
		episode := e.episodes[row.InstanceID]
		if !cpuAtPercent(used, quota, policy.percent) {
			if episode != nil && now.Sub(episode.last) > policy.grace {
				delete(e.episodes, row.InstanceID)
			}
			continue
		}
		if episode == nil {
			episode = &cpuSaturationEpisode{since: now}
			e.episodes[row.InstanceID] = episode
		}
		episode.last = now
		if !episode.acted && now.Sub(episode.since) >= policy.after {
			actions = append(actions, cpuEpisodeAction{row: row, used: used, quota: quota, sustained: now.Sub(episode.since)})
		}
	}
	for instanceID := range e.episodes {
		if _, ok := seen[instanceID]; !ok {
			delete(e.episodes, instanceID)
		}
	}
	return actions
}

// markActed stops the instance's current episode from acting again.
func (e *cpuEpisodes) markActed(instanceID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if episode := e.episodes[instanceID]; episode != nil {
		episode.acted = true
	}
}

// WithCPUSaturationHandler enables automatic CPU captures of saturated
// instances. Nil keeps the poller observation-only.
func (p *Poller) WithCPUSaturationHandler(handler CPUSaturationHandler) *Poller {
	if p != nil {
		p.CPUSaturationHandler = handler
	}
	return p
}

// cpuAtPercent reports whether usedMillicores is at least percent of
// quotaMillicores.
func cpuAtPercent(usedMillicores, quotaMillicores, percent int) bool {
	return quotaMillicores > 0 && usedMillicores*100 >= quotaMillicores*percent
}

// cpuSaturated reports whether usedMillicores is at least
// api.ProfileCPUSaturationPercent of quotaMillicores.
func cpuSaturated(usedMillicores, quotaMillicores int) bool {
	return cpuAtPercent(usedMillicores, quotaMillicores, api.ProfileCPUSaturationPercent)
}

// watchCPUSaturation calls CPUSaturationHandler once per instance whose CPU
// use stays saturated for api.ProfileCPUSaturationAfter. Dips shorter than
// api.ProfileCPUSaturationGrace keep the episode; a longer dip, an unknown
// quota or leaving the snapshot ends it and re-arms the instance.
func (p *Poller) watchCPUSaturation(ctx context.Context, rows []InstanceStat) {
	if p == nil || p.CPUSaturationHandler == nil {
		return
	}
	actions := p.cpuSaturation.due(rows, p.now(), p.CPUSaturationHandler.CPUQuotaMillicores, cpuEpisodePolicy{
		percent: api.ProfileCPUSaturationPercent, after: api.ProfileCPUSaturationAfter, grace: api.ProfileCPUSaturationGrace,
	})
	for _, a := range actions {
		p.runEnforcement("cpu/"+a.row.InstanceID, func() {
			if err := p.CPUSaturationHandler.CPUSaturated(ctx, a.row, a.used, a.quota, a.sustained); err != nil {
				if p.Log != nil {
					p.Log.Warn("instance stats: cpu saturation handler failed", "instance_id", a.row.InstanceID, "app_id", a.row.AppID,
						"used_millicores", a.used, "quota_millicores", a.quota, "err", err)
				}
				return
			}
			p.cpuSaturation.markActed(a.row.InstanceID)
		})
	}
}
