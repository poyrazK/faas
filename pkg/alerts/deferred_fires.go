package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

const alertDeliveryWriteBudget = 5 * time.Second

func (e *Evaluator) deferredFireRecipients(ctx context.Context, ruleID string, firedAt time.Time) (state.AlertDeferredRecipients, bool) {
	store, ok := e.store.(state.AlertDeferredFireStore)
	if !ok || firedAt.IsZero() {
		return state.AlertDeferredRecipients{}, true
	}
	held, err := store.AlertDeferredFireRecipients(ctx, ruleID, firedAt)
	if err != nil {
		e.log.Warn("alerts: read recipients waiting for a stand-down", "rule", ruleID, "err", err)
	}
	return held, err == nil
}

func (e *Evaluator) deliverDeferredAlertFires(ctx context.Context, now time.Time, stats *Stats) {
	store, ok := e.store.(state.AlertDeferredFireStore)
	if !ok {
		return
	}
	batchCtx, cancel := context.WithTimeout(ctx, api.AlertWebhookBudget)
	defer cancel()
	due, err := store.DueDeferredAlertFires(batchCtx, now, api.AlertChannelResolveRetryBatch)
	if err != nil {
		e.log.Warn("alerts: list deferred fires", "err", err)
		return
	}
	pool := newWebhookPool()
	sendCtx := withWebhookPool(batchCtx, pool)
	settledChannels := make(map[string]bool)
	for _, fire := range due {
		if batchCtx.Err() != nil {
			break
		}
		rule, err := e.store.AlertRuleByID(batchCtx, fire.RuleID)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				e.log.Warn("alerts: load rule for deferred fire", "rule", fire.RuleID, "err", err)
				e.retryDeferredFire(ctx, store, fire, now)
			}
			continue
		}
		if settledChannels[fire.DeliveryID] {
			fire.Status = state.AlertDeliveryDelivered
		}
		if !deferredFireCurrent(rule, fire, now) {
			e.finishDeferredFire(ctx, store, rule, fire, now, "the deferred fire was superseded or expired", stats)
			continue
		}
		// Insufficient or unavailable telemetry leaves the live episode open.
		// Keep the original trigger until a check confirms it is still firing.
		if rule.State != state.AlertStateFiring || fire.Blocked {
			e.retryDeferredFire(ctx, store, fire, now)
			continue
		}
		if fire.WebhookURL != "" {
			e.send(sendCtx, stats, func(stats *Stats) {
				if batchCtx.Err() == nil {
					e.deliverDeferredWebhookFire(ctx, batchCtx, store, rule, fire, now, stats)
				}
			})
			continue
		}
		if e.deliverDeferredChannelFire(ctx, batchCtx, store, rule, fire, now, stats) && fire.ChannelOnly {
			settledChannels[fire.DeliveryID] = true
		}
	}
	stats.add(pool.wait())
}

func deferredFireCurrent(rule state.AlertRule, fire state.AlertDeferredFire, now time.Time) bool {
	if !rule.Enabled || rule.ObserveOnly || rule.State == state.AlertStateOk ||
		!rule.LastFiredAt.Truncate(time.Microsecond).Equal(fire.FiredAt.Truncate(time.Microsecond)) ||
		now.Sub(fire.FiredAt) > api.AlertChannelResolveRetryWindow {
		return false
	}
	return fire.WebhookURL == "" || fire.Status == state.AlertDeliveryPending && rule.WebhookURL == fire.WebhookURL
}

func (e *Evaluator) deliverDeferredChannelFire(ctx, batchCtx context.Context, store state.AlertDeferredFireStore, rule state.AlertRule, fire state.AlertDeferredFire, now time.Time, stats *Stats) bool {
	bindings, ok := e.store.(state.AlertRuleChannelStore)
	if !ok || e.channels == nil {
		e.retryDeferredFire(ctx, store, fire, now)
		return false
	}
	channel, found := boundChannel(batchCtx, bindings, rule.ID, fire.ChannelID)
	released := found && e.channelsReleased(batchCtx, rule)
	if batchCtx.Err() != nil {
		e.retryDeferredFire(ctx, store, fire, now)
		return false
	}
	if !released {
		e.finishDeferredFire(ctx, store, rule, fire, now, "the notification channel is no longer available for this rule", stats)
		return false
	}
	if e.channelStandDownPending(batchCtx, rule.ID, fire.ChannelID, true) || e.pagerDutyStandDownPending(batchCtx, rule.ID, channel) {
		e.retryDeferredFire(ctx, store, fire, now)
		return false
	}
	message := e.channelMessage(batchCtx, rule, alertchannels.EventFire, fire.Observed, fire.FiredAt)
	// Preserve the delivery identity when a restart follows a send.
	message.IdempotencyKey = fire.DeliveryID + ":" + fire.ChannelID
	_, delivered := e.sendToChannels(batchCtx, []state.NotificationChannel{channel}, message, now, nil)
	if delivered == 0 {
		e.retryDeferredFire(ctx, store, fire, now)
		return false
	}
	if fire.ChannelOnly && fire.Status == state.AlertDeliveryPending {
		writeCtx, cancel := context.WithTimeout(ctx, alertDeliveryWriteBudget)
		err := e.recordResult(writeCtx, rule, fire.DeliveryID, fire.Observed,
			webhookout.Result{Attempts: 1, StatusCode: http.StatusOK}, now, stats)
		cancel()
		if err != nil {
			return false
		}
	}
	e.finishDeferredFire(ctx, store, rule, fire, now, "", stats)
	stats.ChannelFiresRetried++
	return true
}

func (e *Evaluator) deliverDeferredWebhookFire(ctx, batchCtx context.Context, store state.AlertDeferredFireStore, rule state.AlertRule, fire state.AlertDeferredFire, now time.Time, stats *Stats) {
	var payload map[string]any
	if err := json.Unmarshal(fire.Payload, &payload); err != nil {
		e.finishDeferredFire(ctx, store, rule, fire, now, "the deferred webhook payload is invalid", stats)
		return
	}
	started, err := store.StartDeferredWebhookFire(batchCtx, fire.ID)
	if err != nil {
		e.log.Warn("alerts: start deferred webhook", "rule", rule.ID, "err", err)
		e.retryDeferredFire(ctx, store, fire, now)
		return
	}
	if !started {
		return // Its eligibility changed. The next pass reads the new state.
	}
	if e.sendAlertWebhook(ctx, batchCtx, rule, fire.DeliveryID, fire.Observed, payload, now, stats) {
		e.finishDeferredFire(ctx, store, rule, fire, now, "", stats)
		return
	}
	e.retryDeferredFire(ctx, store, fire, now)
}

func (e *Evaluator) retryDeferredFire(ctx context.Context, store state.AlertDeferredFireStore, fire state.AlertDeferredFire, now time.Time) {
	writeCtx, cancel := context.WithTimeout(ctx, alertDeliveryWriteBudget)
	defer cancel()
	if err := store.RetryDeferredAlertFire(writeCtx, fire.ID, now.Add(fireRetryBackoff(1))); err != nil {
		e.log.Warn("alerts: reschedule deferred fire", "rule", fire.RuleID, "err", err)
	}
}

func (e *Evaluator) finishDeferredFire(ctx context.Context, store state.AlertDeferredFireStore, rule state.AlertRule, fire state.AlertDeferredFire, now time.Time, failure string, stats *Stats) {
	writeCtx, cancel := context.WithTimeout(ctx, alertDeliveryWriteBudget)
	defer cancel()
	last, err := store.FinishDeferredAlertFire(writeCtx, fire.ID)
	if err != nil {
		e.log.Warn("alerts: finish deferred fire", "rule", fire.RuleID, "err", err)
		return
	}
	if failure != "" && fire.Status == state.AlertDeliveryPending && (fire.WebhookURL != "" || fire.ChannelOnly && last) {
		_ = e.recordResult(writeCtx, rule, fire.DeliveryID, fire.Observed, webhookout.Result{Err: errors.New(failure)}, now, stats)
	}
}
