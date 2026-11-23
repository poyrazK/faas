package alerts

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// startWebhookStandDowns snapshots existing terminal events before this tick
// can claim any fires. Transport runs alongside the account evaluations.
func (e *Evaluator) startWebhookStandDowns(ctx context.Context, now time.Time) <-chan Stats {
	result := make(chan Stats, 1)
	store, ok := e.store.(state.AlertWebhookStandDownStore)
	if !ok {
		result <- Stats{}
		return result
	}
	readCtx, cancel := context.WithTimeout(ctx, api.AlertObserveBudget)
	due, err := store.DueAlertWebhookStandDowns(readCtx, now, api.AlertChannelResolveRetryBatch)
	cancel()
	if err != nil {
		e.log.Warn("alerts: list webhook stand-downs", "err", err)
	}
	if len(due) == 0 || err != nil {
		result <- Stats{}
		return result
	}
	go func(ctx context.Context) {
		result <- e.deliverWebhookStandDowns(ctx, store, now, due)
	}(ctx)
	return result
}

func (e *Evaluator) deliverWebhookStandDowns(ctx context.Context, store state.AlertWebhookStandDownStore, now time.Time, due []state.AlertWebhookStandDown) Stats {
	batchCtx, cancel := context.WithTimeout(ctx, api.AlertWebhookBudget)
	defer cancel()
	var stats Stats
	pool := newWebhookPool()
	sendCtx := withWebhookPool(batchCtx, pool)
	for _, notice := range due {
		if batchCtx.Err() != nil {
			break
		}
		e.send(sendCtx, &stats, func(stats *Stats) {
			if batchCtx.Err() != nil {
				return
			}
			expired := now.Sub(notice.QueuedAt) > api.AlertChannelResolveRetryWindow
			if expired || e.sendResolve(batchCtx, notice.Rule, nil, state.StandDownDisabled, notice.ID, notice.QueuedAt, now, stats) {
				if err := store.DeleteAlertWebhookStandDown(ctx, notice.ID); err != nil {
					e.log.Warn("alerts: remove webhook stand-down", "rule", notice.Rule.ID, "err", err)
				} else if expired {
					e.log.Warn("alerts: gave up on a webhook recovery notice", "rule", notice.Rule.ID, "attempts", notice.Attempts)
				}
				return
			}
			if ctx.Err() == nil {
				next := now.Add(resolveRetryBackoff(notice.Attempts + 1))
				if err := store.RetryAlertWebhookStandDown(ctx, notice.ID, next); err != nil {
					e.log.Warn("alerts: defer webhook stand-down", "rule", notice.Rule.ID, "err", err)
				}
			}
		})
	}
	stats.add(pool.wait())
	return stats
}
