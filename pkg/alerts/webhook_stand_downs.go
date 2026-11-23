package alerts

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// deliverWebhookStandDowns gives prior episodes their terminal webhook before
// evaluating the current rules. The whole pass shares one delivery budget.
func (e *Evaluator) deliverWebhookStandDowns(ctx context.Context, now time.Time, stats *Stats) {
	store, ok := e.store.(state.AlertWebhookStandDownStore)
	if !ok {
		return
	}
	batchCtx, cancel := context.WithTimeout(ctx, api.AlertWebhookBudget)
	defer cancel()
	due, err := store.DueAlertWebhookStandDowns(batchCtx, now, api.AlertChannelResolveRetryBatch)
	if err != nil {
		e.log.Warn("alerts: list webhook stand-downs", "err", err)
		return
	}
	if len(due) == 0 {
		return
	}
	pool := newWebhookPool()
	sendCtx := withWebhookPool(batchCtx, pool)
	for _, notice := range due {
		if batchCtx.Err() != nil {
			break
		}
		e.send(sendCtx, stats, func(stats *Stats) {
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
}
