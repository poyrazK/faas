package flagnotify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Runner timing.
const (
	// Interval is how often the runner looks for due notifications.
	Interval = time.Minute
	// Batch bounds the notifications one pass sends.
	Batch = 50
	// Lease is how long a claimed batch stays leased.
	Lease = 5 * time.Minute
	// SendBudget bounds one notification's send and settle (the alert
	// channels' 30 s send budget plus margin): no send starts later than
	// SendBudget before the lease ends.
	SendBudget = 45 * time.Second
	// NotReleasedRetry is when a notification of an account without flag
	// notifications is looked at again.
	NotReleasedRetry = time.Hour
	// SweepBatch bounds the settled notifications one pass deletes.
	SweepBatch = 500
)

// Outcomes counted per notification.
const (
	OutcomeDelivered   = "delivered"
	OutcomeRetry       = "retry"
	OutcomeAbandoned   = "abandoned"
	OutcomeNotReleased = "not_released"
)

// ChannelSender delivers a message to notification channels and returns
// the IDs of those that did not accept it; the alert evaluator implements
// it with the alerting seal domain's identities.
type ChannelSender interface {
	SendToChannelsFailed(ctx context.Context, chans []state.NotificationChannel, msg alertchannels.Message, now time.Time) (failed []string)
}

// Runner claims due flag notifications and sends them.
type Runner struct {
	Store    state.FlagNotificationStore
	Channels ChannelSender
	// Released reports whether the notification's account has flag
	// notifications; nil releases every account.
	Released func(ctx context.Context, n state.FlagNotification) bool
	Log      *slog.Logger
	Now      func() time.Time
	Outcome  func(outcome string)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Runner) count(outcome string) {
	if r.Outcome != nil {
		r.Outcome(outcome)
	}
}

// Run loops until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.log().Warn("flag notifications: tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce sends one batch of due notifications, sweeps old settled ones and
// returns how many it settled. It starts a send only while the batch's
// lease has room for it and hands the rest back at once, so another meterd
// never reclaims a row this one is still sending.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	now := r.now()
	retention := time.Duration(api.FlagsNotificationRetentionDays) * 24 * time.Hour
	if _, err := r.Store.SweepFlagNotifications(ctx, now.Add(-retention), SweepBatch); err != nil {
		r.log().Warn("flag notifications: sweep", "err", err)
	}
	due, err := r.Store.ClaimDueFlagNotifications(ctx, now, now.Add(Lease), Batch)
	if err != nil {
		return 0, fmt.Errorf("claim flag notifications: %w", err)
	}
	sendUntil := now.Add(Lease - SendBudget)
	for _, n := range due {
		var settle state.FlagNotificationSettle
		if at := r.now(); at.After(sendUntil) {
			settle = handBack(n, at, at)
		} else {
			settle = r.deliver(ctx, n, now, retention)
		}
		if err := r.Store.SettleFlagNotification(ctx, settle); err != nil {
			r.log().Warn("flag notifications: settle", "notification", n.ID, "err", err)
		}
	}
	return len(due), nil
}

// handBack returns a claimed notification untried: pending at next, with
// the claim's attempt not counted.
func handBack(n state.FlagNotification, at, next time.Time) state.FlagNotificationSettle {
	return state.FlagNotificationSettle{
		ID: n.ID, Status: state.FlagNotificationPending, LastError: n.LastError, NextAttemptAt: next, Now: at,
		ClaimedAttempts: n.Attempts, Attempts: max(0, n.Attempts-1), ChannelIDs: n.ChannelIDs,
	}
}

// deliver sends one notification and returns its settlement.
func (r *Runner) deliver(ctx context.Context, n state.FlagNotification, now time.Time, retention time.Duration) state.FlagNotificationSettle {
	at := r.now()
	out := state.FlagNotificationSettle{ID: n.ID, Status: state.FlagNotificationPending, Now: at,
		ClaimedAttempts: n.Attempts, Attempts: n.Attempts, ChannelIDs: n.ChannelIDs}
	if now.Sub(n.CreatedAt) > retention {
		r.count(OutcomeAbandoned)
		out.Status, out.LastError = state.FlagNotificationAbandoned, "not sent within the retention period"
		return out
	}
	if r.Released != nil && !r.Released(ctx, n) {
		// Waiting for the release is not a delivery attempt.
		r.count(OutcomeNotReleased)
		out = handBack(n, at, at.Add(NotReleasedRetry))
		out.LastError = "flag notifications are not enabled for this account; nothing was sent"
		return out
	}
	chans, err := r.Store.FlagNotificationChannels(ctx, n.AccountID, n.ChannelIDs)
	if err != nil {
		return r.retry(out, n, at, "channels unavailable")
	}
	if len(chans) == 0 {
		r.count(OutcomeAbandoned)
		out.Status, out.LastError = state.FlagNotificationAbandoned, "none of the notification's channels exist any more"
		return out
	}
	failed := r.Channels.SendToChannelsFailed(ctx, chans, Message(n, at), at)
	if len(failed) == 0 {
		r.count(OutcomeDelivered)
		out.Status = state.FlagNotificationDelivered
		return out
	}
	// Retry only the channels that did not accept it: the ones that did
	// are not sent it again.
	out.ChannelIDs = failed
	reason := "no channel accepted the notification"
	if len(failed) < len(chans) {
		reason = fmt.Sprintf("%d of %d channels did not accept the notification", len(failed), len(chans))
	}
	return r.retry(out, n, at, reason)
}

// retry schedules the next attempt with exponential backoff, or gives up
// after FlagsNotificationMaxAttempts.
func (r *Runner) retry(out state.FlagNotificationSettle, n state.FlagNotification, at time.Time, reason string) state.FlagNotificationSettle {
	out.LastError = reason
	if n.Attempts >= api.FlagsNotificationMaxAttempts {
		r.count(OutcomeAbandoned)
		out.Status = state.FlagNotificationAbandoned
		return out
	}
	r.count(OutcomeRetry)
	out.NextAttemptAt = at.Add(Backoff(n.Attempts))
	return out
}

// Backoff is the wait after the given number of failed attempts: 1, 2, 4,
// 8, … minutes, at most an hour.
func Backoff(attempts int) time.Duration {
	d := time.Minute << max(0, attempts-1)
	return min(d, time.Hour)
}

// Message renders a notification for the alert channels: problems as
// alerts, everything else as a digest.
func Message(n state.FlagNotification, now time.Time) alertchannels.Message {
	event := alertchannels.EventDigest
	if n.Kind == state.FlagNotifyExperimentProblem {
		event = alertchannels.EventFire
	}
	return alertchannels.Message{
		Event: event, RuleName: n.Subject, Summary: n.Summary, OccurredAt: now, Severity: "warning",
		DedupKey: "flag-notification-" + n.ID, IdempotencyKey: "flag-notification-" + n.ID,
		Footer: "You receive this because Gregale flag notifications send to this channel; the message's last line says how to change that.",
	}
}
