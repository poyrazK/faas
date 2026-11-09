package alerts

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An alert episode can end without its rule recovering (ADR-20261104142830):
// the rule is disabled, its plan, flags or account stop covering it, its app
// is deleted, or the rule itself is deleted. Each of those used to leave the
// PagerDuty incident the fire opened, and the Slack thread, without a
// recovery notice. The evaluator now stands such a rule down: it queues a
// notice for every channel the episode notified, sends the webhook's
// alert.resolved when the rule asks for one, and turns the rule ok. A
// deleted rule's notices are queued by a trigger, since the evaluator never
// sees it again. Queued notices are delivered and retried like a recovery
// notice, for up to AlertChannelResolveRetryWindow.

// standDownNotes explain, per reason, why a notice arrives without the
// metric recovering.
var standDownNotes = map[string]string{
	state.StandDownDisabled:    "The rule was disabled, so Gregale stopped evaluating it.",
	state.StandDownNotReleased: "The account's plan or features no longer cover this rule, so Gregale stopped evaluating it.",
	state.StandDownAppDeleted:  "The app was deleted, so Gregale stopped evaluating this rule.",
	state.StandDownDeleted:     "The rule was deleted.",
}

// standDown ends a not-ok rule the evaluator no longer evaluates. The
// notices are queued before the rule turns ok, so a failure in between
// leaves the rule stranded and the next tick tries again.
func (e *Evaluator) standDown(ctx context.Context, rule state.AlertRule, reason string, now time.Time, stats *Stats) {
	// MCP readiness rules run their own incident lifecycle and notices.
	if rule.State == state.AlertStateOk || string(rule.Metric) == api.MetricMCPReadinessUnhealthy {
		return
	}
	wasFiring := e.episodeWasFiring(ctx, rule)
	e.clearChannelFires(rule.ID)
	if store, ok := e.store.(state.AlertChannelStandDownStore); ok {
		if _, err := store.QueueAlertChannelStandDowns(ctx, rule.ID, reason, now); err != nil {
			e.log.Warn("alerts: queue channel stand-downs", "rule", rule.ID, "err", err)
			return
		}
	}
	changed, err := e.store.SetAlertRuleState(ctx, rule.ID, state.AlertStateOk, now)
	if err != nil {
		e.log.Warn("alerts: stand down rule", "rule", rule.ID, "err", err)
		return
	}
	if !changed || !wasFiring {
		return
	}
	stats.StoodDown++
	if e.audit != nil {
		e.audit.Emit(ctx, "alert.resolved", &rule.AccountID, map[string]any{
			"rule_id": rule.ID, "rule": rule.Name, "reason": reason,
		})
	}
	if rule.NotifyResolved && rule.WebhookURL != "" {
		e.deliverResolve(ctx, rule, nil, reason, now, stats)
	}
}

// episodeWasFiring reports whether a not-ok rule has an open episode: it
// turned firing and has not been ok since, whatever degraded or unknown
// reads came after. Only an episode's end is announced as a recovery.
func (e *Evaluator) episodeWasFiring(ctx context.Context, rule state.AlertRule) bool {
	switch rule.State {
	case state.AlertStateFiring:
		return true
	case state.AlertStateOk:
		return false
	}
	store, ok := e.store.(state.AlertChannelStandDownStore)
	if !ok {
		return false
	}
	open, err := store.AlertRuleEpisodeOpen(ctx, rule.ID)
	if err != nil {
		e.log.Warn("alerts: read open episode", "rule", rule.ID, "err", err)
		return false
	}
	return open
}

// standDownStranded stands down the not-ok rules RunOnce did not list:
// disabled ones and those of deleted apps.
func (e *Evaluator) standDownStranded(ctx context.Context, now time.Time, stats *Stats) {
	store, ok := e.store.(state.AlertChannelStandDownStore)
	if !ok {
		return
	}
	rules, err := store.ListStrandedAlertRules(ctx, api.AlertChannelResolveRetryBatch)
	if err != nil {
		e.log.Warn("alerts: list stranded rules", "err", err)
		return
	}
	for _, rule := range rules {
		if ctx.Err() != nil {
			return
		}
		reason := state.StandDownAppDeleted
		if !rule.Enabled {
			reason = state.StandDownDisabled
		}
		e.standDown(ctx, rule, reason, now, stats)
	}
}

// deliverChannelStandDowns sends the due recovery notices of ended
// episodes, retrying a failure with backoff and giving up after
// AlertChannelResolveRetryWindow. They go out whatever the account's plan:
// they close what an earlier fire opened.
func (e *Evaluator) deliverChannelStandDowns(ctx context.Context, now time.Time) (delivered, abandoned int) {
	store, ok := e.store.(state.AlertChannelStandDownStore)
	chans, ok2 := e.store.(state.NotificationChannelStore)
	if !ok || !ok2 || e.channels == nil {
		return 0, 0
	}
	due, err := store.DueAlertChannelStandDowns(ctx, now, api.AlertChannelResolveRetryBatch)
	if err != nil {
		e.log.Warn("alerts: list channel stand-downs", "err", err)
		return 0, 0
	}
	ids := e.unsealIdentities()
	for _, s := range due {
		if ctx.Err() != nil {
			return delivered, abandoned
		}
		if now.Sub(s.QueuedAt) > api.AlertChannelResolveRetryWindow {
			e.abandonStandDown(ctx, store, s)
			abandoned++
			continue
		}
		ch, err := chans.GetNotificationChannel(ctx, s.AccountID, s.ChannelID)
		if errors.Is(err, state.ErrNotFound) {
			_ = store.DeleteAlertChannelStandDown(ctx, s.RuleID, s.ChannelID)
			continue
		}
		if err != nil {
			e.log.Warn("alerts: read channel for a stand-down", "channel", s.ChannelID, "err", err)
			continue
		}
		errMsg := ""
		if target, ok := openTarget(ids, ch); !ok {
			errMsg = "could not open the channel destination; recreate the channel"
		} else {
			sctx, cancel := context.WithTimeout(ctx, channelSendBudget)
			if err := e.channels.Send(sctx, target, e.standDownMessage(ctx, s, now)); err != nil {
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
			_ = store.DeleteAlertChannelStandDown(ctx, s.RuleID, s.ChannelID)
			delivered++
			continue
		}
		e.auditChannelFailure(ctx, state.AlertRule{ID: s.RuleID, AccountID: s.AccountID}, ch, alertchannels.EventResolve, errMsg)
		if err := store.RecordAlertChannelStandDownAttempt(ctx, s.RuleID, s.ChannelID, now.Add(resolveRetryBackoff(s.Attempts+1)), errMsg); err != nil {
			e.log.Warn("alerts: record channel stand-down attempt", "rule", s.RuleID, "channel", s.ChannelID, "err", err)
		}
	}
	return delivered, abandoned
}

func (e *Evaluator) standDownMessage(ctx context.Context, s state.AlertChannelStandDown, now time.Time) alertchannels.Message {
	msg := alertchannels.Message{
		Event: alertchannels.EventResolve, RuleName: s.RuleName, Metric: s.RuleMetric,
		Summary: standDownNotes[s.Reason], OccurredAt: now, DedupKey: s.RuleID,
		// One notice per rule, channel and queueing, however often a failed
		// attempt is retried.
		IdempotencyKey: s.RuleID + ":" + s.ChannelID + ":stand-down:" + s.QueuedAt.UTC().Format(time.RFC3339Nano),
	}
	if msg.AppSlug = e.appSlug(ctx, s.AppID); msg.AppSlug != "" && e.dashboardBaseURL != "" && s.Reason != state.StandDownAppDeleted {
		msg.DashboardURL = e.dashboardBaseURL + "/dashboard/apps/" + msg.AppSlug
	}
	return msg
}

func (e *Evaluator) abandonStandDown(ctx context.Context, store state.AlertChannelStandDownStore, s state.AlertChannelStandDown) {
	if err := store.DeleteAlertChannelStandDown(ctx, s.RuleID, s.ChannelID); err != nil {
		e.log.Warn("alerts: drop channel stand-down", "rule", s.RuleID, "channel", s.ChannelID, "err", err)
		return
	}
	e.log.Warn("alerts: gave up on a channel recovery notice", "rule", s.RuleID, "channel", s.ChannelID, "attempts", s.Attempts, "reason", s.Reason)
	if e.audit != nil {
		e.audit.Emit(ctx, "notification_channel.resolve_abandoned", &s.AccountID, map[string]any{
			"channel_id": s.ChannelID, "rule_id": s.RuleID, "attempts": s.Attempts, "reason": s.Reason,
		})
	}
}

// clearChannelStandDowns drops the notices a rule still owed: a new fire
// supersedes them, as it does a resolve retry.
func (e *Evaluator) clearChannelStandDowns(ctx context.Context, ruleID string) {
	store, ok := e.store.(state.AlertChannelStandDownStore)
	if !ok {
		return
	}
	if err := store.ClearAlertChannelStandDowns(ctx, ruleID); err != nil {
		e.log.Warn("alerts: clear channel stand-downs", "rule", ruleID, "err", err)
	}
}
