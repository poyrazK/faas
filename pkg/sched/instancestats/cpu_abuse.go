package instancestats

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CPUAbuseHandler turns a long CPU saturation episode into the
// ADR-20261112120603 CPU abuse signal. Both methods run on the tick
// goroutine.
type CPUAbuseHandler interface {
	// CPUQuotaMillicores returns the instance's sustained cgroup CPU quota,
	// false while it is unknown or raised by a startup boost.
	CPUQuotaMillicores(instanceID string, now time.Time) (int, bool)
	// CPUSustained runs once per episode that stayed at
	// api.CPUAbuseSaturationPercent of the quota for
	// api.CPUAbuseSustainedAfter. An error retries it on the next tick.
	CPUSustained(ctx context.Context, row InstanceStat, usedMillicores, quotaMillicores int, sustained time.Duration) error
}

// WithCPUAbuseHandler enables sustained CPU abuse detection. Nil keeps the
// poller observation-only.
func (p *Poller) WithCPUAbuseHandler(handler CPUAbuseHandler) *Poller {
	if p != nil {
		p.CPUAbuseHandler = handler
	}
	return p
}

// watchCPUAbuse calls CPUAbuseHandler once per instance whose CPU use stays
// at api.CPUAbuseSaturationPercent of its quota for
// api.CPUAbuseSustainedAfter. A frozen or quiet-paused instance reports no
// CPU use, so an app that stops receiving requests ends its episode.
func (p *Poller) watchCPUAbuse(ctx context.Context, rows []InstanceStat) {
	if p == nil || p.CPUAbuseHandler == nil {
		return
	}
	actions := p.cpuAbuse.due(rows, p.now(), p.CPUAbuseHandler.CPUQuotaMillicores, cpuEpisodePolicy{
		percent: api.CPUAbuseSaturationPercent, after: api.CPUAbuseSustainedAfter, grace: api.CPUAbuseGrace,
	})
	for _, a := range actions {
		p.runEnforcement("cpu-abuse/"+a.row.InstanceID, func() {
			if err := p.CPUAbuseHandler.CPUSustained(ctx, a.row, a.used, a.quota, a.sustained); err != nil {
				if p.Log != nil {
					p.Log.Warn("instance stats: cpu abuse handler failed", "instance_id", a.row.InstanceID, "app_id", a.row.AppID,
						"used_millicores", a.used, "quota_millicores", a.quota, "err", err)
				}
				return
			}
			p.cpuAbuse.markActed(a.row.InstanceID)
		})
	}
}
