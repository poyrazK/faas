package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pauseFailingCron pauses a due HTTP cron whose recent runs all failed and
// queues the email telling its account (ADR-20261115194000). It reports
// whether the cron was paused, in which case the occurrence is not
// dispatched. A store without the capability, or an error, lets the cron
// fire as before.
func (l *Loop) pauseFailingCron(ctx context.Context, c state.Cron) bool {
	pauser, ok := l.engine.Store().(state.CronFailurePauseStore)
	if !ok {
		return false
	}
	paused, err := pauser.PauseCronIfFailing(ctx, c.ID, api.CronFailurePauseRuns, api.CronFailurePauseSpan, api.PlatformAlertCooldown)
	if err != nil {
		l.log.Warn("cron: check failing runs", "cron_id", c.ID, "err", err)
		return false
	}
	if paused {
		l.log.Info("cron: paused after consecutive failed runs", "cron_id", c.ID, "app_id", c.AppID,
			"runs", api.CronFailurePauseRuns)
	}
	return paused
}
