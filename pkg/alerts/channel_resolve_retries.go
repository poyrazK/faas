package alerts

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Recovery notices a channel did not take are retried (ADR-20261031144636):
// a fire is sent again after the rule's cool-down while the breach lasts, but
// a resolve is sent once, so without a retry a PagerDuty incident opened by a
// fire stays open after a short outage at the moment of recovery.

// trackChannelResolve records a failed recovery notice for retry, or forgets
// one that went through.
func (e *Evaluator) trackChannelResolve(ctx context.Context, rule state.AlertRule, ch state.NotificationChannel, observed float64, now time.Time, errMsg string) {
	retries, ok := e.store.(state.AlertChannelResolveRetryStore)
	if !ok {
		return
	}
	var err error
	if errMsg == "" {
		err = retries.DeleteAlertChannelResolveRetry(ctx, rule.ID, ch.ID)
	} else {
		err = retries.RecordAlertChannelResolveRetry(ctx, state.AlertChannelResolveRetry{
			RuleID: rule.ID, ChannelID: ch.ID, Observed: observed, ResolvedAt: now,
			NextAttemptAt: now.Add(resolveRetryBackoff(1)), LastError: errMsg,
		})
	}
	if err != nil {
		e.log.Warn("alerts: track channel resolve", "rule", rule.ID, "channel", ch.ID, "err", err)
	}
}

func (e *Evaluator) clearChannelResolves(ctx context.Context, ruleID string) {
	retries, ok := e.store.(state.AlertChannelResolveRetryStore)
	if !ok {
		return
	}
	if err := retries.ClearAlertChannelResolveRetries(ctx, ruleID); err != nil {
		e.log.Warn("alerts: clear channel resolve retries", "rule", ruleID, "err", err)
	}
}

// auditChannelFailure puts a failed notification in the account's audit log,
// so each one is visible after the channel's last error is overwritten.
func (e *Evaluator) auditChannelFailure(ctx context.Context, rule state.AlertRule, ch state.NotificationChannel, event, errMsg string) {
	if e.audit == nil {
		return
	}
	e.audit.Emit(ctx, "notification_channel.delivery_failed", &rule.AccountID, map[string]any{
		"channel_id": ch.ID, "kind": ch.Kind, "rule_id": rule.ID, "event": event, "error": errMsg,
	})
}

// retryChannelResolves re-sends the due recovery notices of rules still
// recovered, and gives up on those older than AlertChannelResolveRetryWindow.
func (e *Evaluator) retryChannelResolves(ctx context.Context, now time.Time) (retried, abandoned int) {
	retries, ok := e.store.(state.AlertChannelResolveRetryStore)
	bindings, bound := e.store.(state.AlertRuleChannelStore)
	if !ok || !bound || e.channels == nil {
		return 0, 0
	}
	due, err := retries.DueAlertChannelResolveRetries(ctx, now, api.AlertChannelResolveRetryBatch)
	if err != nil {
		e.log.Warn("alerts: list channel resolve retries", "err", err)
		return 0, 0
	}
	ids := e.unsealIdentities()
	for _, r := range due {
		if ctx.Err() != nil {
			return retried, abandoned
		}
		rule, err := e.store.AlertRuleByID(ctx, r.RuleID)
		if err != nil {
			e.log.Warn("alerts: read rule for channel resolve retry", "rule", r.RuleID, "err", err)
			continue
		}
		if now.Sub(r.ResolvedAt) > api.AlertChannelResolveRetryWindow {
			e.abandonChannelResolve(ctx, retries, rule, r)
			abandoned++
			continue
		}
		if !e.channelsReleased(ctx, rule) {
			continue
		}
		ch, found := boundChannel(ctx, bindings, rule.ID, r.ChannelID)
		if !found {
			_ = retries.DeleteAlertChannelResolveRetry(ctx, r.RuleID, r.ChannelID)
			continue
		}
		errMsg := ""
		if target, ok := openTarget(ids, ch); !ok {
			errMsg = "could not open the channel destination; recreate the channel"
		} else {
			sctx, cancel := context.WithTimeout(ctx, channelSendBudget)
			if err := e.channels.Send(sctx, target, e.channelMessage(ctx, rule, alertchannels.EventResolve, r.Observed, r.ResolvedAt)); err != nil {
				errMsg = err.Error()
			}
			cancel()
		}
		outcome := ChannelOutcomeDelivered
		if errMsg != "" {
			outcome = ChannelOutcomeFailed
		}
		e.recordChannel(ctx, ch, now, errMsg, outcome)
		if errMsg == "" {
			_ = retries.DeleteAlertChannelResolveRetry(ctx, r.RuleID, r.ChannelID)
			retried++
			continue
		}
		if err := retries.RecordAlertChannelResolveRetry(ctx, state.AlertChannelResolveRetry{
			RuleID: r.RuleID, ChannelID: r.ChannelID, Observed: r.Observed, ResolvedAt: r.ResolvedAt,
			NextAttemptAt: now.Add(resolveRetryBackoff(r.Attempts + 1)), LastError: errMsg,
		}); err != nil {
			e.log.Warn("alerts: record channel resolve retry", "rule", r.RuleID, "channel", r.ChannelID, "err", err)
		}
	}
	return retried, abandoned
}

func (e *Evaluator) abandonChannelResolve(ctx context.Context, retries state.AlertChannelResolveRetryStore, rule state.AlertRule, r state.AlertChannelResolveRetry) {
	if err := retries.DeleteAlertChannelResolveRetry(ctx, r.RuleID, r.ChannelID); err != nil {
		e.log.Warn("alerts: drop channel resolve retry", "rule", r.RuleID, "channel", r.ChannelID, "err", err)
		return
	}
	e.log.Warn("alerts: gave up on a channel recovery notice", "rule", r.RuleID, "channel", r.ChannelID, "attempts", r.Attempts)
	if e.audit != nil {
		e.audit.Emit(ctx, "notification_channel.resolve_abandoned", &rule.AccountID, map[string]any{
			"channel_id": r.ChannelID, "rule_id": r.RuleID, "attempts": r.Attempts,
		})
	}
}

// channelsReleased reports whether the account may still use channels; a
// rule its plan no longer covers is left for the window to expire.
func (e *Evaluator) channelsReleased(ctx context.Context, rule state.AlertRule) bool {
	if e.release == nil {
		return true
	}
	release := e.release(ctx, rule.AccountID)
	if release == nil {
		return true
	}
	evaluate, channels := release(ctx, rule)
	return evaluate && channels
}

func boundChannel(ctx context.Context, bindings state.AlertRuleChannelStore, ruleID, channelID string) (state.NotificationChannel, bool) {
	chans, err := bindings.ListAlertRuleChannels(ctx, ruleID)
	if err != nil {
		return state.NotificationChannel{}, false
	}
	for _, ch := range chans {
		if ch.ID == channelID {
			return ch, true
		}
	}
	return state.NotificationChannel{}, false
}

// resolveRetryBackoff is the wait before attempt n+1: a minute, doubling, at
// most AlertChannelResolveRetryBackoffMax.
func resolveRetryBackoff(attempts int) time.Duration {
	return min(time.Minute<<min(max(attempts-1, 0), 10), api.AlertChannelResolveRetryBackoffMax)
}
