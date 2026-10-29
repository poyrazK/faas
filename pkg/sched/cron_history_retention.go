package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// runCronHistoryRetention deletes cron schedule decisions and manual-run
// requests older than api.CronHistoryRetention, in bounded passes. It runs
// on the hourly retention tick; a store error ends the sweep and the next
// tick resumes it.
func (l *Loop) runCronHistoryRetention(ctx context.Context) {
	store, ok := l.engine.store.(state.CronHistoryRetentionStore)
	if !ok {
		return
	}
	before := l.now().Add(-api.CronHistoryRetention)
	deleted := 0
	for range api.CronHistoryPrunePasses {
		n, err := store.PruneCronHistory(ctx, before, api.CronHistoryPruneBatch)
		deleted += n
		if err != nil {
			l.log.Warn("cron history retention: sweep failed", "deleted", deleted, "err", err)
			return
		}
		if n < api.CronHistoryPruneBatch {
			break
		}
	}
	if deleted > 0 {
		l.log.Info("cron history retention: swept", "deleted", deleted)
	}
}
